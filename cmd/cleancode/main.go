package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/angus-lau/cleancode/internal/agents"
	"github.com/angus-lau/cleancode/internal/config"
	"github.com/angus-lau/cleancode/internal/context"
	"github.com/angus-lau/cleancode/internal/query"
	"github.com/angus-lau/cleancode/internal/schema"
	"github.com/angus-lau/cleancode/internal/visualizer"
	"github.com/angus-lau/cleancode/internal/watcher"
	"github.com/spf13/cobra"
)

// ANSI colors
const (
	reset  = "\033[0m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
)

var rootFlag string

var rootCmd = &cobra.Command{
	Use:   "cleancode",
	Short: "AI-powered code review with deep codebase understanding",
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize cleancode in a project",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		configPath := filepath.Join(root, ".cleancode.json")

		if _, err := os.Stat(configPath); err == nil {
			fmt.Printf("%s.cleancode.json already exists%s\n", yellow, reset)
			return nil
		}

		cfg := config.DefaultConfig()

		dbURL, _ := cmd.Flags().GetString("db")
		if dbURL != "" {
			cfg.Schema = &config.SchemaConfig{
				Provider: "postgres",
				URL:      dbURL,
			}
		}

		if err := config.Save(root, cfg); err != nil {
			return err
		}

		fmt.Printf("%sCreated .cleancode.json%s\n", green, reset)
		fmt.Println("  Edit it to configure agents, schema, and ignore patterns.")
		if cfg.Schema != nil {
			fmt.Println("  Schema fetching enabled — run 'cleancode index' to fetch.")
		} else {
			fmt.Println("  To enable schema fetching, add a \"schema\" block with your DB URL.")
		}
		return nil
	},
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Index the codebase",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		cfg, _ := config.Load(root)

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

		// Fetch DB schema if configured
		if cfg.Schema != nil && cfg.Schema.URL != "" {
			fmt.Printf("%sFetching database schema%s ...\n", blue, reset)
			dbSchema, err := schema.Fetch(cfg.Schema.URL)
			if err != nil {
				fmt.Printf("  %sWarning: could not fetch schema: %v%s\n", yellow, err, reset)
			} else {
				if err := schema.SaveToStore(engine.StoreDB(), dbSchema); err != nil {
					fmt.Printf("  %sWarning: could not save schema: %v%s\n", yellow, err, reset)
				} else {
					fmt.Printf("  Tables:  %d\n", len(dbSchema.Tables))
				}
			}
		}

		return nil
	},
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Run parallel review agents on current changes",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)
		cfg, _ := config.Load(root)

		baseBranch, _ := cmd.Flags().GetString("base")
		if baseBranch == "" {
			baseBranch = cfg.BaseBranch
		}

		// Set up output writer — stdout by default, tee to file if --output is set
		var w io.Writer = os.Stdout
		outputPath, _ := cmd.Flags().GetString("output")
		if outputPath != "" {
			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("could not create output file: %w", err)
			}
			defer f.Close()
			w = io.MultiWriter(os.Stdout, f)
		}

		fmt.Fprintf(w, "%sAssembling context%s ...\n", blue, reset)
		assembler := context.NewAssembler(root)
		ctx, err := assembler.AssembleDiffContext(baseBranch)
		if err != nil {
			return err
		}

		if ctx.Diff == "" {
			fmt.Fprintf(w, "%sNo changes found.%s\n", yellow, reset)
			return nil
		}

		// Enrich with index data
		fmt.Fprintf(w, "%sLoading index%s ...\n", blue, reset)
		var dbSchema *schema.DBSchema
		engine, err := query.NewEngine(root)
		if err != nil {
			fmt.Fprintf(w, "%sWarning: could not load index, reviewing without context: %v%s\n", yellow, err, reset)
		} else {
			// Re-index to pick up any new changes
			if _, err := engine.Index(); err != nil {
				fmt.Fprintf(w, "%sWarning: indexing failed: %v%s\n", yellow, err, reset)
			} else {
				absFiles := assembler.ChangedFilesAbsolute(ctx.ChangedFiles)
				changedSymbols, callers, dependents := engine.EnrichForReview(absFiles)
				assembler.Enrich(ctx, callers, dependents, changedSymbols)
				fmt.Fprintf(w, "  Changed symbols: %d\n", len(changedSymbols))
				fmt.Fprintf(w, "  Symbols with callers: %d\n", len(callers))
				fmt.Fprintf(w, "  Files with dependents: %d\n", len(dependents))
			}

			// Load schema and find referenced tables
			dbSchema, err = schema.LoadFromStore(engine.StoreDB())
			if err == nil && dbSchema != nil {
				referenced := dbSchema.FindReferencedTables(ctx.Diff)
				if len(referenced) > 0 {
					var schemaStr string
					for _, t := range referenced {
						schemaStr += schema.FormatTable(&t) + "\n"
					}
					ctx.SchemaContext = schemaStr
					fmt.Fprintf(w, "  Referenced tables: %d\n", len(referenced))
				}
			}

			engine.Close()
		}

		// Run deterministic schema validation on the diff
		var schemaFindings []agents.Finding
		if dbSchema != nil {
			validationResults := schema.ValidateDiff(ctx.Diff, dbSchema)
			if len(validationResults) > 0 {
				fmt.Fprintf(w, "%sSchema validation: %d issue(s)%s\n", yellow, len(validationResults), reset)
				for _, v := range validationResults {
					sev := agents.Critical
					schemaFindings = append(schemaFindings, agents.Finding{
						Agent:      "schema-check",
						Severity:   sev,
						File:       v.File,
						Line:       v.Line,
						Message:    v.Message,
						Suggestion: v.Suggestion,
					})
				}
			}
		}

		formatted := context.FormatForAgent(ctx)

		// Build agent list from config (presets + custom)
		var enabledAgents []agents.AgentConfig
		for _, preset := range agents.PresetAgents {
			enabled, exists := cfg.Agents[preset.Name]
			if exists {
				if enabled {
					enabledAgents = append(enabledAgents, preset)
				}
			} else if preset.Enabled {
				enabledAgents = append(enabledAgents, preset)
			}
		}
		for _, custom := range cfg.CustomAgents {
			enabledAgents = append(enabledAgents, agents.AgentConfig{
				Name:    custom.Name,
				Prompt:  custom.Prompt,
				Enabled: true,
			})
		}

		fmt.Fprintf(w, "%sRunning review agents%s ...\n\n", blue, reset)
		orch := agents.NewOrchestrator(enabledAgents)
		results := orch.Review(formatted)

		// Prepend schema-check findings as a separate result
		if len(schemaFindings) > 0 {
			schemaResult := agents.ReviewResult{
				Agent:    "schema-check",
				Findings: schemaFindings,
				Elapsed:  0, // deterministic, instant
			}
			results = append([]agents.ReviewResult{schemaResult}, results...)
		}

		totalFindings := 0
		synthesized := len(results) == 1 && results[0].Agent == "synthesizer"

		if synthesized {
			r := results[0]
			totalFindings = len(r.Findings)
			fmt.Fprintf(w, "%s● synthesized%s %s— %d finding(s) (%dms)%s\n", blue, reset, gray, totalFindings, r.Elapsed, reset)

			for _, f := range r.Findings {
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

				fmt.Fprintf(w, "  %s %s%s%s %s[%s]%s\n", sevStr, cyan, loc, reset, gray, f.Agent, reset)
				fmt.Fprintf(w, "    %s\n", f.Message)
				if f.Suggestion != "" {
					fmt.Fprintf(w, "    %s→ %s%s\n", gray, f.Suggestion, reset)
				}
				fmt.Fprintln(w)
			}
		} else {
			for _, result := range results {
				count := len(result.Findings)
				totalFindings += count

				if count == 0 {
					fmt.Fprintf(w, "%s✓ %s%s %s(%dms)%s\n", green, result.Agent, reset, gray, result.Elapsed, reset)
				} else {
					fmt.Fprintf(w, "%s● %s%s %s— %d finding(s) (%dms)%s\n", yellow, result.Agent, reset, gray, count, result.Elapsed, reset)
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

					fmt.Fprintf(w, "  %s %s%s%s\n", sevStr, cyan, loc, reset)
					fmt.Fprintf(w, "    %s\n", f.Message)
					if f.Suggestion != "" {
						fmt.Fprintf(w, "    %s→ %s%s\n", gray, f.Suggestion, reset)
					}
					fmt.Fprintln(w)
				}
			}
		}

		if totalFindings == 0 {
			fmt.Fprintf(w, "\n%sAll clear! No issues found.%s\n", green, reset)
		} else if synthesized {
			fmt.Fprintf(w, "\n%s%d finding(s), deduplicated and prioritized.%s\n", yellow, totalFindings, reset)
		} else {
			fmt.Fprintf(w, "\n%s%d finding(s) across %d agents.%s\n", yellow, totalFindings, len(results), reset)
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

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Open an interactive dependency graph in the browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)

		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		focus, _ := cmd.Flags().GetString("focus")

		fmt.Printf("%sLoading graph data%s ...\n", blue, reset)
		symbols, edges, err := engine.GraphData()
		if err != nil {
			return err
		}

		if focus != "" {
			fmt.Printf("  Focused on: %s (2 hops)\n", focus)
		}
		fmt.Printf("  %d symbols, %d edges\n", len(symbols), len(edges))
		fmt.Printf("%sOpening in browser%s ...\n", blue, reset)

		return visualizer.GenerateHTML(symbols, edges, root, focus)
	},
}

var explainCmd = &cobra.Command{
	Use:   "explain <symbol>",
	Short: "AI-powered explanation of a symbol with full codebase context",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)

		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}
		defer engine.Close()

		fmt.Printf("%sLooking up %s%s%s ...\n", blue, cyan, args[0], reset)

		symCtx, err := engine.GetSymbolContext(args[0])
		if err != nil {
			return err
		}

		// Format context for Claude
		var b strings.Builder
		fmt.Fprintf(&b, "## Symbol: %s\n", symCtx.Symbol.Name)
		fmt.Fprintf(&b, "- Kind: %s\n", symCtx.Symbol.Kind)
		fmt.Fprintf(&b, "- File: %s:%d-%d\n", symCtx.Symbol.FilePath, symCtx.Symbol.StartLine, symCtx.Symbol.EndLine)

		if symCtx.Source != "" {
			// Cap source at 5000 chars
			src := symCtx.Source
			if len(src) > 5000 {
				src = src[:5000] + "\n... (truncated)"
			}
			fmt.Fprintf(&b, "\n## Source Code\n```\n%s\n```\n", src)
		}

		if len(symCtx.Callers) > 0 {
			fmt.Fprintf(&b, "\n## Callers (%d)\n", len(symCtx.Callers))
			for i, c := range symCtx.Callers {
				if i >= 15 {
					fmt.Fprintf(&b, "- ... and %d more\n", len(symCtx.Callers)-15)
					break
				}
				fmt.Fprintf(&b, "- %s (%s) at %s:%d\n", c.Symbol.Name, c.Symbol.Kind, c.Symbol.FilePath, c.CallLine)
			}
		}

		if len(symCtx.Dependents) > 0 {
			fmt.Fprintf(&b, "\n## Files That Import This File (%d)\n", len(symCtx.Dependents))
			for i, d := range symCtx.Dependents {
				if i >= 10 {
					fmt.Fprintf(&b, "- ... and %d more\n", len(symCtx.Dependents)-10)
					break
				}
				fmt.Fprintf(&b, "- %s (imports: %s)\n", d.FilePath, strings.Join(d.Imports, ", "))
			}
		}

		// Load schema context if available
		dbSchema, schemaErr := schema.LoadFromStore(engine.StoreDB())
		if schemaErr == nil && dbSchema != nil {
			// Check if the source references any tables
			referenced := dbSchema.FindReferencedTables(symCtx.Source)
			if len(referenced) > 0 {
				fmt.Fprintf(&b, "\n## Referenced Database Tables\n")
				for _, t := range referenced {
					b.WriteString(schema.FormatTable(&t))
					b.WriteString("\n")
				}
			}
		}

		fmt.Printf("%sAsking Claude%s ...\n\n", blue, reset)

		explanation, err := agents.Explain(b.String())
		if err != nil {
			return err
		}

		fmt.Println(explanation)
		return nil
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch for file changes and re-index automatically",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, _ := filepath.Abs(rootFlag)

		fmt.Printf("%sStarting watch mode%s for %s\n", blue, reset, root)

		// Initial index
		engine, err := query.NewEngine(root)
		if err != nil {
			return err
		}

		result, err := engine.Index()
		if err != nil {
			return err
		}
		fmt.Printf("%sInitial index:%s %d files, %d symbols, %d edges (%s)\n",
			green, reset, result.Files, result.Symbols, result.Edges, result.Elapsed)

		w, err := watcher.New(root, engine)
		if err != nil {
			engine.Close()
			return err
		}
		defer func() {
			w.Close()
			engine.Close()
		}()

		fmt.Printf("%sWatching for changes%s (Ctrl+C to stop)\n", blue, reset)
		return w.Watch(func(files, symbols, edges int, elapsed time.Duration) {
			fmt.Printf("  %sRe-indexed:%s %d files, %d symbols, %d edges (%s)\n",
				green, reset, files, symbols, edges, elapsed)
		})
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&rootFlag, "root", "r", ".", "Project root directory")
	reviewCmd.Flags().StringP("base", "b", "", "Base branch to diff against (default from config)")
	reviewCmd.Flags().StringP("output", "o", "", "Write review output to file (in addition to stdout)")
	initCmd.Flags().String("db", "", "Database connection string for schema fetching")
	graphCmd.Flags().StringP("focus", "f", "", "Focus on a file or symbol (shows 2-hop neighborhood)")

	rootCmd.AddCommand(initCmd, indexCmd, reviewCmd, searchCmd, callersCmd, statsCmd, hookCmd, watchCmd, explainCmd, graphCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
