package schema

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	_ "github.com/lib/pq"
)

type Column struct {
	Name       string `json:"name"`
	DataType   string `json:"dataType"`
	IsNullable bool   `json:"isNullable"`
	IsPrimary  bool   `json:"isPrimary"`
	Default    string `json:"default,omitempty"`
}

type Table struct {
	Name    string   `json:"name"`
	Columns []Column `json:"columns"`
}

type DBSchema struct {
	Tables []Table `json:"tables"`
}

// Fetch connects to the database and retrieves the public schema.
func Fetch(connStr string) (*DBSchema, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Get all columns
	rows, err := db.Query(`
		SELECT
			c.table_name,
			c.column_name,
			c.data_type,
			c.is_nullable,
			COALESCE(c.column_default, ''),
			CASE WHEN pk.column_name IS NOT NULL THEN true ELSE false END as is_primary
		FROM information_schema.columns c
		LEFT JOIN (
			SELECT ku.table_name, ku.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage ku
				ON tc.constraint_name = ku.constraint_name
			WHERE tc.constraint_type = 'PRIMARY KEY'
				AND tc.table_schema = 'public'
		) pk ON c.table_name = pk.table_name AND c.column_name = pk.column_name
		WHERE c.table_schema = 'public'
		ORDER BY c.table_name, c.ordinal_position
	`)
	if err != nil {
		return nil, fmt.Errorf("querying schema: %w", err)
	}
	defer rows.Close()

	tableMap := make(map[string]*Table)
	var tableOrder []string

	for rows.Next() {
		var tableName, colName, dataType, isNullable, colDefault string
		var isPrimary bool

		if err := rows.Scan(&tableName, &colName, &dataType, &isNullable, &colDefault, &isPrimary); err != nil {
			return nil, err
		}

		if _, exists := tableMap[tableName]; !exists {
			tableMap[tableName] = &Table{Name: tableName}
			tableOrder = append(tableOrder, tableName)
		}

		tableMap[tableName].Columns = append(tableMap[tableName].Columns, Column{
			Name:       colName,
			DataType:   dataType,
			IsNullable: isNullable == "YES",
			IsPrimary:  isPrimary,
			Default:    colDefault,
		})
	}

	sort.Strings(tableOrder)
	tables := make([]Table, 0, len(tableOrder))
	for _, name := range tableOrder {
		tables = append(tables, *tableMap[name])
	}

	return &DBSchema{Tables: tables}, nil
}

// FormatTable returns a human-readable representation of a table schema.
func FormatTable(t *Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s\n", t.Name)
	for _, col := range t.Columns {
		nullable := ""
		if !col.IsNullable {
			nullable = " NOT NULL"
		}
		pk := ""
		if col.IsPrimary {
			pk = " (PK)"
		}
		def := ""
		if col.Default != "" {
			def = fmt.Sprintf(" DEFAULT %s", col.Default)
		}
		fmt.Fprintf(&b, "- %s: %s%s%s%s\n", col.Name, col.DataType, nullable, pk, def)
	}
	return b.String()
}

// FormatSchema returns the full schema as a string.
func FormatSchema(s *DBSchema) string {
	var b strings.Builder
	for i, t := range s.Tables {
		b.WriteString(FormatTable(&t))
		if i < len(s.Tables)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// GetTable returns a single table by name, or nil if not found.
func (s *DBSchema) GetTable(name string) *Table {
	lower := strings.ToLower(name)
	for i, t := range s.Tables {
		if strings.ToLower(t.Name) == lower {
			return &s.Tables[i]
		}
	}
	return nil
}

// FindReferencedTables scans text (like a diff) for table names from the schema
// and returns the matching tables.
func (s *DBSchema) FindReferencedTables(text string) []Table {
	lower := strings.ToLower(text)
	var found []Table
	seen := make(map[string]bool)

	for _, t := range s.Tables {
		if seen[t.Name] {
			continue
		}
		// Look for table name in the text (as a whole word-ish match)
		if strings.Contains(lower, strings.ToLower(t.Name)) {
			found = append(found, t)
			seen[t.Name] = true
		}
	}
	return found
}
