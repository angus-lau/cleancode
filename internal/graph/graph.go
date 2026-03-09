package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/angus/cleancode/internal/indexer"
)

var tsExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

type DependencyGraph struct {
	files         map[string]*indexer.FileNode
	symbolIndex   map[string]indexer.Symbol  // "filePath:name" -> Symbol
	importerIndex map[string]map[string]bool // filePath -> set of importer filePaths
	edges         []indexer.Edge
}

func New() *DependencyGraph {
	return &DependencyGraph{
		files:         make(map[string]*indexer.FileNode),
		symbolIndex:   make(map[string]indexer.Symbol),
		importerIndex: make(map[string]map[string]bool),
	}
}

func (g *DependencyGraph) AddFile(file *indexer.FileNode) {
	g.files[file.Path] = file
	for _, sym := range file.Symbols {
		id := fmt.Sprintf("%s:%s:%d", file.Path, sym.Name, sym.StartLine)
		g.symbolIndex[id] = sym
	}
}

func (g *DependencyGraph) BuildEdges() {
	g.edges = nil
	g.importerIndex = make(map[string]map[string]bool)

	for filePath, file := range g.files {
		for _, imp := range file.Imports {
			resolved := resolveImport(imp.Source, filePath)
			if resolved == "" {
				continue
			}

			// Track file-level import relationship
			if g.importerIndex[resolved] == nil {
				g.importerIndex[resolved] = make(map[string]bool)
			}
			g.importerIndex[resolved][filePath] = true

			// Create edges from local symbols to imported symbols
			for _, spec := range imp.Specifiers {
				// Find target symbol by matching name
				var targetID string
				for id, sym := range g.symbolIndex {
					if sym.FilePath == resolved && sym.Name == spec {
						targetID = id
						break
					}
				}
				if targetID != "" {
					for _, localSym := range file.Symbols {
						localID := fmt.Sprintf("%s:%s:%d", filePath, localSym.Name, localSym.StartLine)
						g.edges = append(g.edges, indexer.Edge{
							From: localID,
							To:   targetID,
							Type: "imports",
						})
					}
				}
			}
		}
	}
}

func (g *DependencyGraph) GetCallers(symbolName string) []indexer.CallerResult {
	// Find the target symbol
	var targetID string
	for id, sym := range g.symbolIndex {
		if sym.Name == symbolName {
			targetID = id
			break
		}
	}
	if targetID == "" {
		return nil
	}

	var results []indexer.CallerResult
	for _, edge := range g.edges {
		if edge.To == targetID {
			if callerSym, ok := g.symbolIndex[edge.From]; ok {
				results = append(results, indexer.CallerResult{
					Symbol:   callerSym,
					CallLine: callerSym.StartLine,
				})
			}
		}
	}
	return results
}

func (g *DependencyGraph) GetDependents(filePath string) []indexer.DependentResult {
	importers := g.importerIndex[filePath]
	if importers == nil {
		return nil
	}

	var results []indexer.DependentResult
	for importerPath := range importers {
		file := g.files[importerPath]
		if file == nil {
			continue
		}

		var relevantSpecifiers []string
		for _, imp := range file.Imports {
			resolved := resolveImport(imp.Source, importerPath)
			if resolved == filePath {
				relevantSpecifiers = append(relevantSpecifiers, imp.Specifiers...)
			}
		}

		results = append(results, indexer.DependentResult{
			FilePath: importerPath,
			Imports:  relevantSpecifiers,
		})
	}
	return results
}

func (g *DependencyGraph) GetDependencies(filePath string) []indexer.DependentResult {
	file := g.files[filePath]
	if file == nil {
		return nil
	}

	var results []indexer.DependentResult
	for _, imp := range file.Imports {
		resolved := resolveImport(imp.Source, filePath)
		if resolved != "" {
			if _, exists := g.files[resolved]; exists {
				results = append(results, indexer.DependentResult{
					FilePath: resolved,
					Imports:  imp.Specifiers,
				})
			}
		}
	}
	return results
}

func (g *DependencyGraph) GetSymbol(name string) (indexer.Symbol, bool) {
	for _, sym := range g.symbolIndex {
		if sym.Name == name {
			return sym, true
		}
	}
	return indexer.Symbol{}, false
}

func (g *DependencyGraph) AllSymbols() []indexer.Symbol {
	symbols := make([]indexer.Symbol, 0, len(g.symbolIndex))
	for _, sym := range g.symbolIndex {
		symbols = append(symbols, sym)
	}
	return symbols
}

func (g *DependencyGraph) Edges() []indexer.Edge {
	return g.edges
}

func (g *DependencyGraph) Stats() indexer.IndexStats {
	return indexer.IndexStats{
		Files:   len(g.files),
		Symbols: len(g.symbolIndex),
		Edges:   len(g.edges),
	}
}

func resolveImport(source, fromFile string) string {
	// Skip bare specifiers (node_modules)
	if !strings.HasPrefix(source, ".") && !strings.HasPrefix(source, "/") {
		return ""
	}

	dir := filepath.Dir(fromFile)
	base := filepath.Join(dir, source)

	// Try exact path
	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		return base
	}

	// Try with extensions
	for _, ext := range tsExtensions {
		withExt := base + ext
		if _, err := os.Stat(withExt); err == nil {
			return withExt
		}
	}

	// Try index files
	for _, ext := range tsExtensions {
		indexPath := filepath.Join(base, "index"+ext)
		if _, err := os.Stat(indexPath); err == nil {
			return indexPath
		}
	}

	return ""
}
