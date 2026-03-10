package schema

import (
	"fmt"
	"regexp"
	"strings"
)

// ColumnRef represents a column reference found in code.
type ColumnRef struct {
	Table  string // resolved table name (or alias)
	Column string
	Line   int    // line number in the diff (0 if unknown)
	File   string // file path
	Raw    string // original matched text (e.g., "p.aura")
}

// ValidationFinding represents a schema validation issue.
type ValidationFinding struct {
	File       string
	Line       int
	Column     string
	Table      string
	Message    string
	Suggestion string
}

// ValidateDiff checks added lines in a diff for column references that don't
// exist in the database schema. It handles:
//   - Raw SQL: SELECT p.col FROM posts p / JOIN posts p
//   - Supabase: .from("posts").select("col1, col2").order("col")
func ValidateDiff(diff string, dbSchema *DBSchema) []ValidationFinding {
	if dbSchema == nil {
		return nil
	}

	// Build a quick lookup: tableName → set of column names
	tableColumns := buildColumnLookup(dbSchema)

	var findings []ValidationFinding

	// Process each file in the diff
	for _, fileDiff := range splitDiffByFile(diff) {
		fileFindinngs := validateFileDiff(fileDiff, tableColumns)
		findings = append(findings, fileFindinngs...)
	}

	return findings
}

// buildColumnLookup builds a map of table_name -> set of column names (lowercase).
func buildColumnLookup(dbSchema *DBSchema) map[string]map[string]bool {
	lookup := make(map[string]map[string]bool)
	for _, table := range dbSchema.Tables {
		cols := make(map[string]bool)
		for _, col := range table.Columns {
			cols[strings.ToLower(col.Name)] = true
		}
		lookup[strings.ToLower(table.Name)] = cols
	}
	return lookup
}

type fileDiff struct {
	path         string
	addedLines   []numberedLine
	contextLines []string // context + added lines (for alias resolution)
}

type numberedLine struct {
	num  int
	text string
}

var diffFileHeader = regexp.MustCompile(`^diff --git a/(.+?) b/`)
var diffHunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// splitDiffByFile splits a unified diff into per-file chunks with only added lines.
func splitDiffByFile(diff string) []fileDiff {
	var result []fileDiff
	var current *fileDiff
	lineNum := 0

	for _, line := range strings.Split(diff, "\n") {
		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			if current != nil {
				result = append(result, *current)
			}
			current = &fileDiff{path: m[1]}
			lineNum = 0
			continue
		}

		if current == nil {
			continue
		}

		if m := diffHunkHeader.FindStringSubmatch(line); m != nil {
			fmt.Sscanf(m[1], "%d", &lineNum)
			continue
		}

		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			text := line[1:] // strip leading +
			current.addedLines = append(current.addedLines, numberedLine{
				num:  lineNum,
				text: text,
			})
			current.contextLines = append(current.contextLines, text)
			lineNum++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			// Removed lines don't increment new-file line counter but are useful for context
		} else if !strings.HasPrefix(line, "\\") {
			// Context line — include for alias resolution, increment counter
			if len(line) > 0 {
				current.contextLines = append(current.contextLines, line)
			}
			lineNum++
		}
	}

	if current != nil {
		result = append(result, *current)
	}
	return result
}

// Patterns for SQL alias extraction: FROM/JOIN table alias
var sqlFromJoin = regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-z_][a-z0-9_]*)\s+([a-z_][a-z0-9_]*)\b`)

// Patterns for qualified column refs: alias.column (excludes method calls like posts.map(...))
var sqlQualifiedCol = regexp.MustCompile(`\b([a-z_][a-z0-9_]*)\.([a-z_][a-z0-9_]*)\b(?:\s*\()?`)

// JS/TS method names that commonly follow a dot — never SQL columns
var jsMethodNames = map[string]bool{
	"map": true, "filter": true, "reduce": true, "find": true,
	"forEach": true, "some": true, "every": true, "includes": true,
	"push": true, "pop": true, "shift": true, "unshift": true,
	"slice": true, "splice": true, "concat": true, "join": true,
	"sort": true, "reverse": true, "flat": true, "flatMap": true,
	"keys": true, "values": true, "entries": true, "length": true,
	"toString": true, "valueOf": true, "hasOwnProperty": true,
	"then": true, "catch": true, "finally": true,
	"trim": true, "toLowerCase": true, "toUpperCase": true,
	"split": true, "replace": true, "match": true, "test": true,
	"parse": true, "stringify": true, "assign": true, "freeze": true,
	"round": true, "floor": true, "ceil": true, "abs": true,
	"max": true, "min": true, "pow": true, "sqrt": true,
	"now": true, "toISOString": true, "getTime": true,
	"exec": true, "query": true, "on": true, "emit": true,
	"get": true, "set": true, "delete": true, "has": true, "add": true,
	"from": true, "of": true, "isArray": true, "isNaN": true,
	"append": true, "remove": true, "insert": true, "update": true,
	"select": true, "single": true, "eq": true, "rpc": true,
	"log": true, "warn": true, "error": true, "info": true, "debug": true,
}

// Patterns for Supabase .from("table")
var supabaseFrom = regexp.MustCompile(`\.from\(\s*["']([a-z_][a-z0-9_]*)["']\s*\)`)

// Patterns for Supabase .select("col1, col2, col3")
var supabaseSelect = regexp.MustCompile(`\.select\(\s*["']([^"']+)["']\s*\)`)

// Patterns for Supabase .order("col"), .gte("col", ...), .not("col", ...), .eq("col", ...), .gt("col", ...)
var supabaseMethod = regexp.MustCompile(`\.(order|gte|gt|lte|lt|eq|neq|not|is|in)\(\s*["']([a-z_][a-z0-9_]*)["']`)

func validateFileDiff(fd fileDiff, tableColumns map[string]map[string]bool) []ValidationFinding {
	// Skip non-code files
	if !isCodeFile(fd.path) {
		return nil
	}

	var findings []ValidationFinding

	// Use all context lines (context + added) for alias/table resolution
	allContext := strings.Join(fd.contextLines, "\n")

	// Build alias → table mapping from full context (includes surrounding lines)
	aliasMap := extractAliases(allContext, tableColumns)

	// Also try to extract Supabase .from("table") context
	supabaseTable := extractSupabaseTable(allContext, tableColumns)

	// Check each added line for column references
	for _, line := range fd.addedLines {
		// 1. Check qualified SQL refs: alias.column
		for _, match := range sqlQualifiedCol.FindAllStringSubmatch(line.text, -1) {
			prefix := strings.ToLower(match[1])
			col := strings.ToLower(match[2])

			// Skip if this looks like a method call: table.method(
			if strings.HasSuffix(match[0], "(") || jsMethodNames[col] {
				continue
			}

			// Skip common non-table prefixes
			if isIgnoredPrefix(prefix) {
				continue
			}

			// Resolve alias to table name
			tableName := prefix
			if resolved, ok := aliasMap[prefix]; ok {
				tableName = resolved
			}

			// Check if this table is in our schema
			cols, tableExists := tableColumns[tableName]
			if !tableExists {
				continue // not a known table, skip
			}

			// Check if column exists
			if !cols[col] {
				suggestion := findSimilarColumn(col, tableColumns[tableName])
				findings = append(findings, ValidationFinding{
					File:       fd.path,
					Line:       line.num,
					Column:     col,
					Table:      tableName,
					Message:    fmt.Sprintf("Column %q does not exist on table %q (referenced as %s.%s)", col, tableName, match[1], match[2]),
					Suggestion: suggestion,
				})
			}
		}

		// 2. Check Supabase .select("col1, col2") columns
		if supabaseTable != "" {
			for _, match := range supabaseSelect.FindAllStringSubmatch(line.text, -1) {
				cols := parseSupabaseSelectCols(match[1])
				for _, col := range cols {
					col = strings.ToLower(col)
					if tableCols, ok := tableColumns[supabaseTable]; ok {
						if !tableCols[col] {
							suggestion := findSimilarColumn(col, tableCols)
							findings = append(findings, ValidationFinding{
								File:       fd.path,
								Line:       line.num,
								Column:     col,
								Table:      supabaseTable,
								Message:    fmt.Sprintf("Column %q does not exist on table %q (in .select() call)", col, supabaseTable),
								Suggestion: suggestion,
							})
						}
					}
				}
			}

			// 3. Check Supabase method columns: .order("col"), .gte("col", ...), etc.
			for _, match := range supabaseMethod.FindAllStringSubmatch(line.text, -1) {
				col := strings.ToLower(match[2])
				if tableCols, ok := tableColumns[supabaseTable]; ok {
					if !tableCols[col] {
						suggestion := findSimilarColumn(col, tableCols)
						findings = append(findings, ValidationFinding{
							File:       fd.path,
							Line:       line.num,
							Column:     col,
							Table:      supabaseTable,
							Message:    fmt.Sprintf("Column %q does not exist on table %q (in .%s() call)", col, supabaseTable, match[1]),
							Suggestion: suggestion,
						})
					}
				}
			}
		}
	}

	return dedup(findings)
}

// extractAliases scans text for FROM/JOIN table alias patterns and returns alias→table map.
func extractAliases(text string, tableColumns map[string]map[string]bool) map[string]string {
	aliases := make(map[string]string)
	for _, match := range sqlFromJoin.FindAllStringSubmatch(text, -1) {
		table := strings.ToLower(match[1])
		alias := strings.ToLower(match[2])

		// Only map if the table actually exists in our schema
		if _, ok := tableColumns[table]; ok {
			// Sanity: alias shouldn't be a SQL keyword
			if !isSQLKeyword(alias) {
				aliases[alias] = table
			}
		}
	}
	return aliases
}

// extractSupabaseTable finds the first .from("table") in text where table exists in schema.
func extractSupabaseTable(text string, tableColumns map[string]map[string]bool) string {
	for _, match := range supabaseFrom.FindAllStringSubmatch(text, -1) {
		table := strings.ToLower(match[1])
		if _, ok := tableColumns[table]; ok {
			return table
		}
	}
	return ""
}

// parseSupabaseSelectCols parses "col1, col2, table!inner(col3)" into column names.
// Handles nested relations by extracting only top-level column names.
func parseSupabaseSelectCols(selectStr string) []string {
	var cols []string
	depth := 0

	current := ""
	for _, ch := range selectStr {
		switch ch {
		case '(':
			depth++
			current = "" // skip nested columns
		case ')':
			depth--
			current = ""
		case ',':
			if depth == 0 {
				col := cleanColumnName(current)
				if col != "" {
					cols = append(cols, col)
				}
				current = ""
			}
		default:
			if depth == 0 {
				current += string(ch)
			}
		}
	}

	// Last column
	if depth == 0 {
		col := cleanColumnName(current)
		if col != "" {
			cols = append(cols, col)
		}
	}

	return cols
}

// cleanColumnName trims whitespace and strips Supabase modifiers like ::text, !inner, etc.
func cleanColumnName(s string) string {
	s = strings.TrimSpace(s)
	// Strip cast: col::text
	if idx := strings.Index(s, "::"); idx > 0 {
		s = s[:idx]
	}
	// Strip relation modifier: table!inner
	if idx := strings.Index(s, "!"); idx > 0 {
		s = s[:idx]
	}
	// Strip alias: col AS alias or col as alias
	lower := strings.ToLower(s)
	if idx := strings.Index(lower, " as "); idx > 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	// Must be a simple identifier
	if !isIdentifier(s) {
		return ""
	}
	return s
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, ch := range s {
		if i == 0 && ch >= '0' && ch <= '9' {
			return false
		}
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

// isCodeFile returns true for files that might contain SQL.
func isCodeFile(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".py", ".go", ".rb", ".rs"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// isIgnoredPrefix returns true for common JS/TS object prefixes that aren't SQL aliases.
var ignoredPrefixes = map[string]bool{
	"this": true, "self": true, "console": true, "process": true,
	"math": true, "json": true, "object": true, "array": true,
	"date": true, "number": true, "string": true, "error": true,
	"promise": true, "buffer": true, "map": true, "set": true,
	"req": true, "res": true, "err": true, "ctx": true,
	"os": true, "fs": true, "path": true, "http": true,
	"fmt": true, "log": true, "time": true, "sync": true,
	"sentry": true, "redis": true, "axios": true,
}

func isIgnoredPrefix(prefix string) bool {
	return ignoredPrefixes[prefix]
}

var sqlKeywords = map[string]bool{
	"where": true, "and": true, "or": true, "on": true, "set": true,
	"into": true, "values": true, "select": true, "from": true,
	"join": true, "left": true, "right": true, "inner": true,
	"outer": true, "cross": true, "order": true, "group": true,
	"having": true, "limit": true, "offset": true, "as": true,
	"case": true, "when": true, "then": true, "else": true,
	"end": true, "not": true, "null": true, "true": true,
	"false": true, "is": true, "in": true, "between": true,
	"like": true, "exists": true, "distinct": true, "union": true,
	"except": true, "intersect": true, "all": true, "any": true,
}

func isSQLKeyword(s string) bool {
	return sqlKeywords[strings.ToLower(s)]
}

// findSimilarColumn suggests a similar column name if one exists (simple edit distance).
func findSimilarColumn(target string, columns map[string]bool) string {
	bestDist := 999
	bestCol := ""

	for col := range columns {
		d := levenshtein(target, col)
		if d < bestDist && d <= 3 { // max 3 edits
			bestDist = d
			bestCol = col
		}
	}

	if bestCol != "" {
		return fmt.Sprintf("Did you mean %q?", bestCol)
	}
	return ""
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// dedup removes duplicate findings (same file, line, column, table).
func dedup(findings []ValidationFinding) []ValidationFinding {
	type key struct {
		file, column, table string
		line                int
	}
	seen := make(map[key]bool)
	var result []ValidationFinding

	for _, f := range findings {
		k := key{f.File, f.Column, f.Table, f.Line}
		if !seen[k] {
			seen[k] = true
			result = append(result, f)
		}
	}
	return result
}
