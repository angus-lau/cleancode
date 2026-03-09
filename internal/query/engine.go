package query

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/angus/cleancode/internal/graph"
	"github.com/angus/cleancode/internal/indexer"
	"github.com/angus/cleancode/internal/storage"
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
	rootPath  string
	extractor *indexer.Extractor
	graph     *graph.DependencyGraph
	store     *storage.Store
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

	symbolCount := 0
	for _, filePath := range files {
		hash, err := e.store.GetFileHash(filePath)
		if err != nil {
			return nil, err
		}

		fileNode, err := e.extractor.ParseFile(filePath)
		if err != nil {
			continue // Skip unparseable files
		}

		// Skip unchanged files
		if hash == fileNode.Hash {
			// Still add to graph for edge building
			e.graph.AddFile(fileNode)
			continue
		}

		e.graph.AddFile(fileNode)
		if err := e.store.SaveFile(fileNode); err != nil {
			return nil, fmt.Errorf("saving %s: %w", filePath, err)
		}
		symbolCount += len(fileNode.Symbols)
	}

	e.graph.BuildEdges()

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

func (e *Engine) Callers(symbolName string) []indexer.CallerResult {
	return e.graph.GetCallers(symbolName)
}

func (e *Engine) Dependents(filePath string) []indexer.DependentResult {
	absPath := filepath.Join(e.rootPath, filePath)
	return e.graph.GetDependents(absPath)
}

func (e *Engine) Dependencies(filePath string) []indexer.DependentResult {
	absPath := filepath.Join(e.rootPath, filePath)
	return e.graph.GetDependencies(absPath)
}

func (e *Engine) Search(query string) []indexer.Symbol {
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

func (e *Engine) Stats() (indexer.IndexStats, error) {
	return e.store.Stats()
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
