package context

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	// MaxContextChars caps the total formatted context sent to each agent.
	// ~50K chars ≈ ~12K tokens, well within context limits.
	MaxContextChars = 50_000
	// MaxDiffChars caps the diff portion to leave room for enrichment.
	MaxDiffChars = 30_000
)

type ReviewContext struct {
	Diff           string
	ChangedFiles   []string
	ChangedSymbols []string
	Callers        map[string][]string
	Dependents     map[string][]string
	SchemaContext  string // formatted DB schema for referenced tables
}

type Assembler struct {
	rootPath string
}

func NewAssembler(rootPath string) *Assembler {
	return &Assembler{rootPath: rootPath}
}

func (a *Assembler) AssembleDiffContext(baseBranch string) (*ReviewContext, error) {
	diff, err := a.getDiff(baseBranch)
	if err != nil {
		return nil, err
	}

	changedFiles := parseChangedFiles(diff)

	return &ReviewContext{
		Diff:           diff,
		ChangedFiles:   changedFiles,
		ChangedSymbols: nil,
		Callers:        make(map[string][]string),
		Dependents:     make(map[string][]string),
	}, nil
}

func (a *Assembler) Enrich(ctx *ReviewContext, callers, dependents map[string][]string, changedSymbols []string) {
	ctx.Callers = callers
	ctx.Dependents = dependents
	ctx.ChangedSymbols = changedSymbols
}

// ChangedFilesAbsolute returns changed file paths as absolute paths.
func (a *Assembler) ChangedFilesAbsolute(changedFiles []string) []string {
	abs := make([]string, len(changedFiles))
	for i, f := range changedFiles {
		if strings.HasPrefix(f, "/") {
			abs[i] = f
		} else {
			abs[i] = a.rootPath + "/" + f
		}
	}
	return abs
}

func FormatForAgent(ctx *ReviewContext) string {
	var b strings.Builder
	budget := MaxContextChars

	// Diff (capped)
	diff := ctx.Diff
	if len(diff) > MaxDiffChars {
		diff = diff[:MaxDiffChars] + "\n... (diff truncated)\n"
	}
	section := "## Diff\n```diff\n" + diff + "\n```\n"
	b.WriteString(section)
	budget -= len(section)

	// Changed symbols
	if len(ctx.ChangedSymbols) > 0 && budget > 500 {
		section = "\n## Changed Symbols\n"
		for _, s := range ctx.ChangedSymbols {
			line := fmt.Sprintf("- %s\n", s)
			if budget-len(section)-len(line) < 0 {
				section += "- ... (truncated)\n"
				break
			}
			section += line
		}
		b.WriteString(section)
		budget -= len(section)
	}

	// Callers
	if len(ctx.Callers) > 0 && budget > 500 {
		section = "\n## Callers of Changed Symbols\n"
		for sym, callerList := range ctx.Callers {
			header := fmt.Sprintf("### %s (%d callers)\n", sym, len(callerList))
			section += header
			for _, c := range callerList {
				line := fmt.Sprintf("- %s\n", c)
				if budget-len(section)-len(line) < 0 {
					section += "- ... (truncated)\n"
					break
				}
				section += line
			}
		}
		b.WriteString(section)
		budget -= len(section)
	}

	// Dependents
	if len(ctx.Dependents) > 0 && budget > 500 {
		section = "\n## Files That Import Changed Files\n"
		for file, deps := range ctx.Dependents {
			header := fmt.Sprintf("### %s\n", file)
			section += header
			for _, d := range deps {
				line := fmt.Sprintf("- %s\n", d)
				if budget-len(section)-len(line) < 0 {
					section += "- ... (truncated)\n"
					break
				}
				section += line
			}
		}
		b.WriteString(section)
	}

	// Database schema
	if ctx.SchemaContext != "" && budget > 500 {
		section = "\n## Database Schema (referenced tables)\n" + ctx.SchemaContext
		if len(section) > budget {
			section = section[:budget-20] + "\n... (truncated)\n"
		}
		b.WriteString(section)
	}

	return b.String()
}

func (a *Assembler) getDiff(baseBranch string) (string, error) {
	cmd := exec.Command("git", "diff", baseBranch+"...HEAD")
	cmd.Dir = a.rootPath
	out, err := cmd.Output()
	if err != nil {
		// Fallback to unstaged diff
		cmd = exec.Command("git", "diff")
		cmd.Dir = a.rootPath
		out, err = cmd.Output()
		if err != nil {
			return "", err
		}
	}
	return string(out), nil
}

var diffFileRegex = regexp.MustCompile(`(?m)^diff --git a/(.+?) b/`)

func parseChangedFiles(diff string) []string {
	matches := diffFileRegex.FindAllStringSubmatch(diff, -1)
	seen := make(map[string]bool)
	var files []string

	for _, m := range matches {
		file := m[1]
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	return files
}
