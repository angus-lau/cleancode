package context

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type ReviewContext struct {
	Diff           string
	ChangedFiles   []string
	ChangedSymbols []string
	Callers        map[string][]string
	Dependents     map[string][]string
	FileContents   map[string]string
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
	fileContents := make(map[string]string)

	for _, file := range changedFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			continue // File may have been deleted
		}
		fileContents[file] = string(content)
	}

	return &ReviewContext{
		Diff:           diff,
		ChangedFiles:   changedFiles,
		ChangedSymbols: nil,
		Callers:        make(map[string][]string),
		Dependents:     make(map[string][]string),
		FileContents:   fileContents,
	}, nil
}

func (a *Assembler) Enrich(ctx *ReviewContext, callers, dependents map[string][]string, changedSymbols []string) {
	ctx.Callers = callers
	ctx.Dependents = dependents
	ctx.ChangedSymbols = changedSymbols
}

func FormatForAgent(ctx *ReviewContext) string {
	var b strings.Builder

	b.WriteString("## Diff\n```diff\n")
	b.WriteString(ctx.Diff)
	b.WriteString("\n```\n")

	if len(ctx.ChangedSymbols) > 0 {
		b.WriteString("\n## Changed Symbols\n")
		for _, s := range ctx.ChangedSymbols {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}

	if len(ctx.Callers) > 0 {
		b.WriteString("\n## Callers of Changed Symbols\n")
		for sym, callerList := range ctx.Callers {
			fmt.Fprintf(&b, "### %s\n", sym)
			for _, c := range callerList {
				fmt.Fprintf(&b, "- %s\n", c)
			}
		}
	}

	if len(ctx.Dependents) > 0 {
		b.WriteString("\n## Files That Import Changed Files\n")
		for file, deps := range ctx.Dependents {
			fmt.Fprintf(&b, "### %s\n", file)
			for _, d := range deps {
				fmt.Fprintf(&b, "- %s\n", d)
			}
		}
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
