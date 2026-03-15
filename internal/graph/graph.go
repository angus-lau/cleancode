package graph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/angus-lau/cleancode/internal/indexer"
)

var tsExtensions = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}

type DependencyGraph struct {
	files         map[string]*indexer.FileNode
	symbolIndex   map[string]indexer.Symbol  // "filePath:name" -> Symbol
	importerIndex map[string]map[string]bool // filePath -> set of importer filePaths
	edges         []indexer.Edge
	goModulePath  string // Go module path from go.mod (e.g., "github.com/foo/bar")
	goModuleRoot  string // Absolute path to go.mod directory
}

func New() *DependencyGraph {
	return &DependencyGraph{
		files:         make(map[string]*indexer.FileNode),
		symbolIndex:   make(map[string]indexer.Symbol),
		importerIndex: make(map[string]map[string]bool),
	}
}

func (g *DependencyGraph) SetGoModule(modulePath, moduleRoot string) {
	g.goModulePath = modulePath
	g.goModuleRoot = moduleRoot
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
		for i := range file.Imports {
			imp := &file.Imports[i]
			resolved := g.resolveImport(imp.Source, filePath)
			if resolved == "" {
				continue
			}

			// Persist the resolved path back onto the ImportRef
			imp.ResolvedPath = resolved

			// Check if resolved is a directory (Go package)
			isDir := false
			if info, err := os.Stat(resolved); err == nil && info.IsDir() {
				isDir = true
			}

			// Track file-level import relationship
			if isDir {
				// Go package: register each indexed file in the directory as imported
				for path := range g.files {
					if strings.HasPrefix(path, resolved+string(filepath.Separator)) {
						if g.importerIndex[path] == nil {
							g.importerIndex[path] = make(map[string]bool)
						}
						g.importerIndex[path][filePath] = true
					}
				}
			} else {
				if g.importerIndex[resolved] == nil {
					g.importerIndex[resolved] = make(map[string]bool)
				}
				g.importerIndex[resolved][filePath] = true
			}

			// Build a lookup of specifier -> target symbol ID for this import
			// Also index "Class.method" style references to method symbols
			specToTarget := make(map[string]string)
			for _, spec := range imp.Specifiers {
				for id, sym := range g.symbolIndex {
					if isDir {
						// Go package: match symbols in files under the resolved directory.
						// References are "pkg.Symbol" (e.g., "indexer.ParseFile").
						if !strings.HasPrefix(sym.FilePath, resolved+string(filepath.Separator)) {
							continue
						}
						specToTarget[spec+"."+sym.Name] = id
					} else {
						if sym.FilePath == resolved && sym.Name == spec {
							specToTarget[spec] = id
						}
						// Also map "Class.method" references to method symbols
						// Method symbols are stored as "ClassName.methodName"
						if sym.FilePath == resolved && sym.Kind == indexer.Method &&
							strings.HasPrefix(sym.Name, spec+".") {
							specToTarget[sym.Name] = id
						}
					}
				}
			}

			// Create precise edges: only link a symbol to an import it actually references
			for _, localSym := range file.Symbols {
				localID := fmt.Sprintf("%s:%s:%d", filePath, localSym.Name, localSym.StartLine)

				if len(localSym.References) > 0 {
					for _, ref := range localSym.References {
						if targetID, ok := specToTarget[ref]; ok {
							g.edges = append(g.edges, indexer.Edge{
								From: localID,
								To:   targetID,
								Type: "calls",
							})
						}
					}
				}
				// No fallback — all symbol kinds now have References populated via AST walking
			}
		}
	}
}

func (g *DependencyGraph) GetCallers(symbolName string) []indexer.CallerResult {
	// Find all target symbol IDs matching this name.
	// Match both exact name and method names (e.g. "batchGetFollowStates"
	// matches "FollowService.batchGetFollowStates").
	targetIDs := make(map[string]bool)
	for id, sym := range g.symbolIndex {
		if sym.Name == symbolName || (sym.Kind == indexer.Method && strings.HasSuffix(sym.Name, "."+symbolName)) {
			targetIDs[id] = true
		}
	}
	if len(targetIDs) == 0 {
		return nil
	}

	var results []indexer.CallerResult
	for _, edge := range g.edges {
		if targetIDs[edge.To] {
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
			resolved := g.resolveImport(imp.Source, importerPath)
			if resolved == filePath || strings.HasPrefix(filePath, resolved+string(filepath.Separator)) {
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
		resolved := g.resolveImport(imp.Source, filePath)
		if resolved == "" {
			continue
		}
		if _, exists := g.files[resolved]; exists {
			results = append(results, indexer.DependentResult{
				FilePath: resolved,
				Imports:  imp.Specifiers,
			})
		} else {
			// Go package directory: check if any indexed file is inside
			for path := range g.files {
				if strings.HasPrefix(path, resolved+string(filepath.Separator)) {
					results = append(results, indexer.DependentResult{
						FilePath: resolved,
						Imports:  imp.Specifiers,
					})
					break
				}
			}
		}
	}
	return results
}

func (g *DependencyGraph) SymbolsInFile(filePath string) []indexer.Symbol {
	var symbols []indexer.Symbol
	for _, sym := range g.symbolIndex {
		if sym.FilePath == filePath {
			symbols = append(symbols, sym)
		}
	}
	return symbols
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

// Files returns the internal file map (used to persist resolved import paths).
func (g *DependencyGraph) Files() map[string]*indexer.FileNode {
	return g.files
}

func (g *DependencyGraph) Stats() indexer.IndexStats {
	return indexer.IndexStats{
		Files:   len(g.files),
		Symbols: len(g.symbolIndex),
		Edges:   len(g.edges),
	}
}

func (g *DependencyGraph) resolveImport(source, fromFile string) string {
	// Go module path resolution: map module imports to local directories
	if g.goModulePath != "" && strings.HasPrefix(source, g.goModulePath+"/") {
		relPath := strings.TrimPrefix(source, g.goModulePath)
		dirPath := filepath.Join(g.goModuleRoot, relPath)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			return dirPath
		}
	}

	// Skip bare specifiers (node_modules, stdlib, external packages)
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
