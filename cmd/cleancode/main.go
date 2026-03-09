package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/angus/cleancode/internal/agents"
	"github.com/angus/cleancode/internal/context"
	"github.com/angus/cleancode/internal/query"
	"github.com/spf13/cobra"
)

// ANSI colors
const (
	reset   = "\033[0m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	cyan    = "\033[36m"
	gray    = "\033[90m"
)

var rootFlag string

var rootCmd = &cobra.Command{
	Use:   "cleancode",
	Short: "AI-powered code review with deep codebase understanding",
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index the codebase",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		fmt.Printf("%sIndexing%s %s ...\n", blue, reset, root)

		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		result, err := engine.Index()
		if err != nil {
			return err
		}

		fmt.Printf("%sDone!%s\n", green, reset)
		fmt.Printf("  Files:   %d\n", result.Files)
		fmt.Printf("  Symbols: %d\n", result.Symbols)
		fmt.Printf("  Edges:   %d\n", result.Edges)
		fmt.Printf("  Time:    %s\n", result.Elapsed)
		return nil
	},
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Run parallel review agents on current changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		baseBranch, _ := cmd.Flags().GetString("base")

		fmt.Printf("%sAssembling context%s ...\n", blue, reset)
		assembler := context.NewAssembler(root)
		ctx, err := assembler.AssembleDiffContext(baseBranch)
		if err != nil {
			return err
		}

		if ctx.Diff == "" {
			fmt.Printf("%sNo changes found.%s\n", yellow, reset)
			return nil
		}

		// Enrich with index data
		fmt.Printf("%sLoading index%s ...\n", blue, reset)
		engine, err := query.NewEngine(root)
		if err != nil {
			fmt.Printf("%sWarning: could not load index, reviewing without context: %v%s\n", yellow, err, reset)
		} else {
			// Re-index to pick up any new changes
			if _, err := engine.Index(); err != nil {
				fmt.Printf("%sWarning: indexing failed: %v%s\n", yellow, err, reset)
			} else {
				absFiles := assembler.ChangedFilesAbsolute(ctx.ChangedFiles)
				changedSymbols, callers, dependents := engine.EnrichForReview(absFiles)
				assembler.Enrich(ctx, callers, dependents, changedSymbols)
				fmt.Printf("  Changed symbols: %d\n", len(changedSymbols))
				fmt.Printf("  Symbols with callers: %d\n", len(callers))
				fmt.Printf("  Files with dependents: %d\n", len(dependents))
			}
			engine.Close()
		}

		formatted := context.FormatForAgent(ctx)

		fmt.Printf("%sRunning review agents%s ...\n\n", blue, reset)
		orch := agents.NewOrchestrator(nil)
		results := orch.Review(formatted)

		totalFindings := 0
		for _, result := range results {
			count := len(result.Findings)
			totalFindings += count

			if count == 0 {
				fmt.Printf("%s✓ %s%s %s(%dms)%s\n", green, result.Agent, reset, gray, result.Elapsed, reset)
			} else {
				fmt.Printf("%s● %s%s %s— %d finding(s) (%dms)%s\n", yellow, result.Agent, reset, gray, count, result.Elapsed, reset)
			}

			for _, f := range result.Findings {
				var sevStr string
				switch f.Severity {
				case agents.Critical:
					sevStr = red + "CRITICAL" + reset
				case agents.Warning:
					sevStr = yellow + "WARNING" + reset
				default:
					sevStr = gray + "INFO" + reset
				}

				loc := f.File
				if f.Line > 0 {
					loc = fmt.Sprintf("%s:%d", f.File, f.Line)
				}

				fmt.Printf("  %s %s%s%s\n", sevStr, cyan, loc, reset)
				fmt.Printf("    %s\n", f.Message)
				if f.Suggestion != "" {
					fmt.Printf("    %s→ %s%s\n", gray, f.Suggestion, reset)
				}
				fmt.Println()
			}
		}

		if totalFindings == 0 {
			fmt.Printf("\n%sAll clear! No issues found.%s\n", green, reset)
		} else {
			fmt.Printf("\n%s%d finding(s) across %d agents.%s\n", yellow, totalFindings, len(results), reset)
		}

		return nil
	},
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search for symbols in the index",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		results := engine.Search(args[0])
		if len(results) == 0 {
			fmt.Printf("%sNo symbols found matching%s %s%s%s\n", yellow, reset, cyan, args[0], reset)
			return nil
		}

		limit := 20
		if len(results) < limit {
			limit = len(results)
		}
		for _, sym := range results[:limit] {
			fmt.Printf("%s%-10s%s %s  %s%s:%d%s\n",
				gray, sym.Kind, reset,
				sym.Name,
				gray, sym.FilePath, sym.StartLine, reset)
		}
		if len(results) > 20 {
			fmt.Printf("%s... and %d more%s\n", gray, len(results)-20, reset)
		}
		return nil
	},
}

var callersCmd = &cobra.Command{
	Use:   "callers <symbol>",
	Short: "Find all callers of a symbol",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		results := engine.Callers(args[0])
		if len(results) == 0 {
			fmt.Printf("%sNo callers found for%s %s%s%s\n", yellow, reset, cyan, args[0], reset)
			return nil
		}

		fmt.Printf("%sCallers of %s:%s\n", blue, args[0], reset)
		for _, r := range results {
			fmt.Printf("  %s%-10s%s %s  %s%s:%d%s\n",
				gray, r.Symbol.Kind, reset,
				r.Symbol.Name,
				gray, r.Symbol.FilePath, r.CallLine, reset)
		}
		return nil
	},
}

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show index statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		stats, err := engine.Stats()
		if err != nil {
			return err
		}

		fmt.Printf("%sIndex Stats%s\n", blue, reset)
		fmt.Printf("  Files:   %d\n", stats.Files)
		fmt.Printf("  Symbols: %d\n", stats.Symbols)
		fmt.Printf("  Edges:   %d\n", stats.Edges)
		return nil
	},
}

var hookCmd = &cobra.Command{
	Use:   "hook <install|remove>",
	Short: "Install or remove git pre-push hook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		hookPath := filepath.Join(root, ".git", "hooks", "pre-push")

		switch args[0] {
		case "install":
			script := fmt.Sprintf("#!/bin/sh\n# cleancode pre-push review\necho \"Running cleancode review...\"\ncleancode review --root \"%s\"\n", root)
			if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
				return err
			}
			fmt.Printf("%sPre-push hook installed!%s\n", green, reset)

		case "remove":
			if err := os.Remove(hookPath); err != nil {
				if os.IsNotExist(err) {
					fmt.Printf("%sNo hook found.%s\n", yellow, reset)
					return nil
				}
				return err
			}
			fmt.Printf("%sPre-push hook removed.%s\n", green, reset)

		default:
			return fmt.Errorf("usage: cleancode hook <install|remove>")
		}
		return nil
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&rootFlag, "root", "r", ".", "Project root directory")
	reviewCmd.Flags().StringP("base", "b", "main", "Base branch to diff against")

	rootCmd.AddCommand(indexCmd, reviewCmd, searchCmd, callersCmd, statsCmd, hookCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
