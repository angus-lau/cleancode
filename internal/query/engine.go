package query

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/angus-lau/cleancode/internal/graph"
	"github.com/angus-lau/cleancode/internal/indexer"
	"github.com/angus-lau/cleancode/internal/storage"
)

var (
	sourcePatterns = []string{"*.ts", "*.tsx", "*.js", "*.jsx", "*.mjs", "*.cjs", "*.py", "*.go"}
	ignoreDirs     = map[string]bool{
		"node_modules":  true,
		"dist":          true,
		"build":         true,
		".git":          true,
		"coverage":      true,
		".cleancode":    true,
		"__pycache__":   true,
		"venv":          true,
		".venv":         true,
		"vendor":        true,
	}
)

type Engine struct {
	rootPath    string
	extractor   *indexer.Extractor
	graph       *graph.DependencyGraph
	store       *storage.Store
	graphLoaded bool // true after Index() has been called
}

func NewEngine(rootPath string) (*Engine, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	store, err := storage.New(absRoot)
	if err != nil {
		return nil, err
	}

	return &Engine{
		rootPath:  absRoot,
		extractor: indexer.NewExtractor(),
		graph:     graph.New(),
		store:     store,
	}, nil
}

type IndexResult struct {
	Files   int
	Symbols int
	Edges   int
	Elapsed time.Duration
}

func (e *Engine) Index() (*IndexResult, error) {
	start := time.Now()

	files, err := e.findSourceFiles()
	if err != nil {
		return nil, err
	}

	// Prune files that no longer exist on disk
	currentFiles := make(map[string]bool, len(files))
	for _, f := range files {
		currentFiles[f] = true
	}
	if pruned, err := e.store.PruneDeletedFiles(currentFiles); err != nil {
		return nil, fmt.Errorf("pruning deleted files: %w", err)
	} else if pruned > 0 {
		fmt.Printf("  Pruned %d deleted file(s) from index\n", pruned)
	}

	// Bulk-load all stored hashes in one query (avoids N+1)
	storedHashes, err := e.store.GetAllFileHashes()
	if err != nil {
		return nil, fmt.Errorf("loading file hashes: %w", err)
	}

	symbolCount := 0
	for _, filePath := range files {
		// Cheap hash check: compute MD5 without tree-sitter parsing
		currentHash, err := indexer.FileHash(filePath)
		if err != nil {
			continue // Skip unreadable files
		}

		// Unchanged file: load from SQLite instead of re-parsing
		if storedHashes[filePath] == currentHash {
			cached, err := e.store.LoadFile(filePath)
			if err == nil {
				e.graph.AddFile(cached)
				continue
			}
			// If load fails, fall through to re-parse
		}

		fileNode, err := e.extractor.ParseFile(filePath)
		if err != nil {
			continue // Skip unparseable files
		}

		e.graph.AddFile(fileNode)
		if err := e.store.SaveFile(fileNode); err != nil {
			return nil, fmt.Errorf("saving %s: %w", filePath, err)
		}
		symbolCount += len(fileNode.Symbols)
	}

	e.graph.BuildEdges()
	e.graphLoaded = true

	// Persist resolved import paths to SQLite (must happen after BuildEdges resolves them)
	if err := e.store.SaveResolvedPaths(e.graph.Files()); err != nil {
		return nil, fmt.Errorf("saving resolved paths: %w", err)
	}

	// Persist edges to SQLite
	if err := e.store.SaveEdges(e.graph.Edges()); err != nil {
		return nil, fmt.Errorf("saving edges: %w", err)
	}

	stats := e.graph.Stats()
	return &IndexResult{
		Files:   stats.Files,
		Symbols: stats.Symbols,
		Edges:   stats.Edges,
		Elapsed: time.Since(start),
	}, nil
}

// EnrichForReview takes a list of changed files (absolute paths) and returns
// the changed symbols, their callers, and file-level dependents.
func (e *Engine) EnrichForReview(changedFiles []string) (changedSymbols []string, callers map[string][]string, dependents map[string][]string) {
	callers = make(map[string][]string)
	dependents = make(map[string][]string)

	const maxCallersPerSymbol = 10
	const maxDependentsPerFile = 10

	for _, filePath := range changedFiles {
		// Get symbols defined in this file
		symbols := e.graph.SymbolsInFile(filePath)
		for _, sym := range symbols {
			changedSymbols = append(changedSymbols, fmt.Sprintf("%s (%s, %s:%d)", sym.Name, sym.Kind, sym.FilePath, sym.StartLine))

			// Get callers for each symbol
			symCallers := e.graph.GetCallers(sym.Name)
			if len(symCallers) > 0 {
				var callerStrs []string
				for i, c := range symCallers {
					if i >= maxCallersPerSymbol {
						callerStrs = append(callerStrs, fmt.Sprintf("... and %d more", len(symCallers)-maxCallersPerSymbol))
						break
					}
					callerStrs = append(callerStrs, fmt.Sprintf("%s (%s, %s:%d)", c.Symbol.Name, c.Symbol.Kind, c.Symbol.FilePath, c.CallLine))
				}
				callers[sym.Name] = callerStrs
			}
		}

		// Get file-level dependents
		deps := e.graph.GetDependents(filePath)
		if len(deps) > 0 {
			var depStrs []string
			for i, d := range deps {
				if i >= maxDependentsPerFile {
					depStrs = append(depStrs, fmt.Sprintf("... and %d more", len(deps)-maxDependentsPerFile))
					break
				}
				depStrs = append(depStrs, fmt.Sprintf("%s (imports: %s)", d.FilePath, strings.Join(d.Imports, ", ")))
			}
			dependents[filePath] = depStrs
		}
	}

	return
}

func (e *Engine) Callers(symbolName string) []indexer.CallerResult {
	if e.graphLoaded {
		return e.graph.GetCallers(symbolName)
	}
	// Query from SQLite
	results, err := e.store.GetCallersOf(symbolName)
	if err != nil {
		return nil
	}
	return results
}

func (e *Engine) Dependents(filePath string) []indexer.DependentResult {
	absPath := filepath.Join(e.rootPath, filePath)
	if e.graphLoaded {
		return e.graph.GetDependents(absPath)
	}
	results, err := e.store.GetDependentsOf(absPath)
	if err != nil {
		return nil
	}
	return results
}

func (e *Engine) Dependencies(filePath string) []indexer.DependentResult {
	if e.graphLoaded {
		absPath := filepath.Join(e.rootPath, filePath)
		return e.graph.GetDependencies(absPath)
	}
	return nil // TODO: add DB read path for dependencies
}

func (e *Engine) Search(query string) []indexer.Symbol {
	if e.graphLoaded {
		all := e.graph.AllSymbols()
		lower := strings.ToLower(query)
		var results []indexer.Symbol
		for _, sym := range all {
			if strings.Contains(strings.ToLower(sym.Name), lower) {
				results = append(results, sym)
			}
		}
		return results
	}
	// Query from SQLite
	results, err := e.store.SearchSymbols(query)
	if err != nil {
		return nil
	}
	return results
}

// SymbolContext holds everything needed for explaining a symbol.
type SymbolContext struct {
	Symbol     indexer.Symbol
	Source     string // actual source code
	Callers    []indexer.CallerResult
	Dependents []indexer.DependentResult
}

// GetSymbolContext finds a symbol and gathers its callers and dependents.
func (e *Engine) GetSymbolContext(symbolName string) (*SymbolContext, error) {
	// Find the symbol
	results := e.Search(symbolName)
	if len(results) == 0 {
		return nil, fmt.Errorf("symbol %q not found", symbolName)
	}

	// Pick the best match (exact name match first, then first result)
	var sym indexer.Symbol
	found := false
	for _, s := range results {
		if s.Name == symbolName {
			sym = s
			found = true
			break
		}
	}
	if !found {
		sym = results[0]
	}

	// Read the source code for this symbol
	source := ""
	content, err := os.ReadFile(sym.FilePath)
	if err == nil {
		lines := strings.Split(string(content), "\n")
		start := sym.StartLine - 1
		end := sym.EndLine
		if start < 0 {
			start = 0
		}
		if end > len(lines) {
			end = len(lines)
		}
		source = strings.Join(lines[start:end], "\n")
	}

	// Get callers
	callers := e.Callers(sym.Name)

	// Get dependents of the file this symbol is in
	relPath := sym.FilePath
	if strings.HasPrefix(relPath, e.rootPath) {
		relPath = strings.TrimPrefix(relPath, e.rootPath+"/")
	}
	dependents := e.Dependents(relPath)

	return &SymbolContext{
		Symbol:     sym,
		Source:     source,
		Callers:    callers,
		Dependents: dependents,
	}, nil
}

// GraphData returns all symbols and edges for visualization.
func (e *Engine) GraphData() ([]indexer.Symbol, []indexer.Edge, error) {
	symbols, err := e.store.AllSymbols()
	if err != nil {
		return nil, nil, err
	}
	edges, err := e.store.AllEdges()
	if err != nil {
		return nil, nil, err
	}
	return symbols, edges, nil
}

func (e *Engine) Stats() (indexer.IndexStats, error) {
	return e.store.Stats()
}

// StoreDB returns the underlying SQLite database handle for use by other packages (e.g. schema storage).
func (e *Engine) StoreDB() *sql.DB {
	return e.store.DB()
}

func (e *Engine) Close() error {
	return e.store.Close()
}

func (e *Engine) findSourceFiles() ([]string, error) {
	var files []string

	err := filepath.WalkDir(e.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if d.IsDir() {
			if ignoreDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		for _, pattern := range sourcePatterns {
			matched, _ := filepath.Match(pattern, d.Name())
			if matched {
				files = append(files, path)
				break
			}
		}
		return nil
	})

	return files, err
}
