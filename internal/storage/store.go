package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/angus-lau/cleancode/internal/indexer"
	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

func New(rootPath string) (*Store, error) {
	storeDir := filepath.Join(rootPath, ".cleancode")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", filepath.Join(storeDir, "index.db?_journal_mode=WAL&_foreign_keys=on"))
	if err != nil {
		return nil, err
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			last_modified INTEGER NOT NULL,
			hash TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS symbols (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			file_path TEXT NOT NULL REFERENCES files(path) ON DELETE CASCADE,
			start_line INTEGER NOT NULL,
			end_line INTEGER NOT NULL,
			export_name TEXT
		);

		CREATE TABLE IF NOT EXISTS imports (
			file_path TEXT NOT NULL REFERENCES files(path) ON DELETE CASCADE,
			source TEXT NOT NULL,
			specifiers TEXT NOT NULL,
			is_default INTEGER NOT NULL DEFAULT 0,
			is_namespace INTEGER NOT NULL DEFAULT 0,
			resolved_path TEXT
		);

		CREATE TABLE IF NOT EXISTS edges (
			from_id TEXT NOT NULL,
			to_id TEXT NOT NULL,
			type TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
		CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file_path);
		CREATE INDEX IF NOT EXISTS idx_imports_file ON imports(file_path);
		CREATE INDEX IF NOT EXISTS idx_imports_resolved ON imports(resolved_path);
		CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_id);
		CREATE INDEX IF NOT EXISTS idx_edges_to ON edges(to_id);
	`)
	return err
}

// PruneDeletedFiles removes files from the index that no longer exist on disk.
// Symbols and imports cascade-delete via foreign keys.
func (s *Store) PruneDeletedFiles(currentFiles map[string]bool) (int, error) {
	rows, err := s.db.Query("SELECT path FROM files")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return 0, err
		}
		if !currentFiles[path] {
			stale = append(stale, path)
		}
	}

	if len(stale) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	for _, path := range stale {
		tx.Exec("DELETE FROM files WHERE path = ?", path)
	}

	return len(stale), tx.Commit()
}

func (s *Store) SaveFile(file *indexer.FileNode) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert file
	_, err = tx.Exec("INSERT OR REPLACE INTO files (path, last_modified, hash) VALUES (?, ?, ?)",
		file.Path, file.LastModified, file.Hash)
	if err != nil {
		return err
	}

	// Clear old data
	tx.Exec("DELETE FROM symbols WHERE file_path = ?", file.Path)
	tx.Exec("DELETE FROM imports WHERE file_path = ?", file.Path)

	// Insert symbols
	for _, sym := range file.Symbols {
		id := fmt.Sprintf("%s:%s:%d", file.Path, sym.Name, sym.StartLine)
		var exportName *string
		if sym.ExportName != "" {
			exportName = &sym.ExportName
		}
		_, err = tx.Exec("INSERT INTO symbols (id, name, kind, file_path, start_line, end_line, export_name) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id, sym.Name, string(sym.Kind), file.Path, sym.StartLine, sym.EndLine, exportName)
		if err != nil {
			return err
		}
	}

	// Insert imports
	for _, imp := range file.Imports {
		specJSON, _ := json.Marshal(imp.Specifiers)
		isDefault := 0
		if imp.IsDefault {
			isDefault = 1
		}
		isNamespace := 0
		if imp.IsNamespace {
			isNamespace = 1
		}
		_, err = tx.Exec("INSERT INTO imports (file_path, source, specifiers, is_default, is_namespace, resolved_path) VALUES (?, ?, ?, ?, ?, ?)",
			file.Path, imp.Source, string(specJSON), isDefault, isNamespace, imp.ResolvedPath)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// SaveResolvedPaths updates the resolved_path column for all imports after graph resolution.
func (s *Store) SaveResolvedPaths(files map[string]*indexer.FileNode) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE imports SET resolved_path = ? WHERE file_path = ? AND source = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, file := range files {
		for _, imp := range file.Imports {
			if imp.ResolvedPath != "" {
				stmt.Exec(imp.ResolvedPath, file.Path, imp.Source)
			}
		}
	}

	return tx.Commit()
}

func (s *Store) SaveEdges(edges []indexer.Edge) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM edges")
	for _, edge := range edges {
		_, err = tx.Exec("INSERT INTO edges (from_id, to_id, type) VALUES (?, ?, ?)",
			edge.From, edge.To, edge.Type)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchSymbols finds symbols whose name contains the query (case-insensitive).
func (s *Store) SearchSymbols(query string) ([]indexer.Symbol, error) {
	rows, err := s.db.Query(
		"SELECT name, kind, file_path, start_line, end_line, export_name FROM symbols WHERE name LIKE ? LIMIT 100",
		"%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []indexer.Symbol
	for rows.Next() {
		var sym indexer.Symbol
		var kind string
		var exportName sql.NullString
		if err := rows.Scan(&sym.Name, &kind, &sym.FilePath, &sym.StartLine, &sym.EndLine, &exportName); err != nil {
			return nil, err
		}
		sym.Kind = indexer.SymbolKind(kind)
		if exportName.Valid {
			sym.ExportName = exportName.String
		}
		symbols = append(symbols, sym)
	}
	return symbols, nil
}

// GetCallersOf finds all symbols that reference the given symbol name via edges.
// Also matches method names (e.g. "batchGetFollowStates" matches "FollowService.batchGetFollowStates").
func (s *Store) GetCallersOf(symbolName string) ([]indexer.CallerResult, error) {
	rows, err := s.db.Query(`
		SELECT s.name, s.kind, s.file_path, s.start_line, s.end_line
		FROM edges e
		JOIN symbols s ON s.id = e.from_id
		WHERE e.to_id IN (SELECT id FROM symbols WHERE name = ? OR (kind = 'method' AND name LIKE '%.' || ?))
		LIMIT 50
	`, symbolName, symbolName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []indexer.CallerResult
	for rows.Next() {
		var sym indexer.Symbol
		var kind string
		if err := rows.Scan(&sym.Name, &kind, &sym.FilePath, &sym.StartLine, &sym.EndLine); err != nil {
			return nil, err
		}
		sym.Kind = indexer.SymbolKind(kind)
		results = append(results, indexer.CallerResult{
			Symbol:   sym,
			CallLine: sym.StartLine,
		})
	}
	return results, nil
}

// GetSymbolsInFile returns all symbols defined in a given file.
func (s *Store) GetSymbolsInFile(filePath string) ([]indexer.Symbol, error) {
	rows, err := s.db.Query(
		"SELECT name, kind, file_path, start_line, end_line, export_name FROM symbols WHERE file_path = ?",
		filePath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []indexer.Symbol
	for rows.Next() {
		var sym indexer.Symbol
		var kind string
		var exportName sql.NullString
		if err := rows.Scan(&sym.Name, &kind, &sym.FilePath, &sym.StartLine, &sym.EndLine, &exportName); err != nil {
			return nil, err
		}
		sym.Kind = indexer.SymbolKind(kind)
		if exportName.Valid {
			sym.ExportName = exportName.String
		}
		symbols = append(symbols, sym)
	}
	return symbols, nil
}

// GetDependentsOf returns files that import the given file path.
func (s *Store) GetDependentsOf(filePath string) ([]indexer.DependentResult, error) {
	rows, err := s.db.Query(
		"SELECT file_path, specifiers FROM imports WHERE resolved_path = ?",
		filePath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resultsMap := make(map[string][]string)
	for rows.Next() {
		var importerPath, specJSON string
		if err := rows.Scan(&importerPath, &specJSON); err != nil {
			return nil, err
		}
		var specs []string
		json.Unmarshal([]byte(specJSON), &specs)
		resultsMap[importerPath] = append(resultsMap[importerPath], specs...)
	}

	var results []indexer.DependentResult
	for fp, imports := range resultsMap {
		results = append(results, indexer.DependentResult{FilePath: fp, Imports: imports})
	}
	return results, nil
}

// HasIndex returns true if the database has indexed data.
func (s *Store) HasIndex() bool {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count)
	return count > 0
}

func (s *Store) GetFileHash(path string) (string, error) {
	var hash string
	err := s.db.QueryRow("SELECT hash FROM files WHERE path = ?", path).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (s *Store) Stats() (indexer.IndexStats, error) {
	var stats indexer.IndexStats
	s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&stats.Files)
	s.db.QueryRow("SELECT COUNT(*) FROM symbols").Scan(&stats.Symbols)
	s.db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&stats.Edges)
	return stats, nil
}

// AllSymbols returns every symbol in the index.
func (s *Store) AllSymbols() ([]indexer.Symbol, error) {
	rows, err := s.db.Query("SELECT name, kind, file_path, start_line, end_line FROM symbols")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var symbols []indexer.Symbol
	for rows.Next() {
		var sym indexer.Symbol
		var kind string
		if err := rows.Scan(&sym.Name, &kind, &sym.FilePath, &sym.StartLine, &sym.EndLine); err != nil {
			return nil, err
		}
		sym.Kind = indexer.SymbolKind(kind)
		symbols = append(symbols, sym)
	}
	return symbols, nil
}

// AllEdges returns every edge in the index.
func (s *Store) AllEdges() ([]indexer.Edge, error) {
	rows, err := s.db.Query("SELECT from_id, to_id, type FROM edges")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []indexer.Edge
	for rows.Next() {
		var e indexer.Edge
		if err := rows.Scan(&e.From, &e.To, &e.Type); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, nil
}

// DB returns the underlying database handle for use by other packages.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	return s.db.Close()
}
