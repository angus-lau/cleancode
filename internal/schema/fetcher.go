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
	// Ensure connection timeout
	if !strings.Contains(connStr, "connect_timeout") {
		sep := "?"
		if strings.Contains(connStr, "?") {
			sep = "&"
		}
		connStr += sep + "connect_timeout=10"
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Set a generous statement timeout for information_schema queries
	db.Exec("SET statement_timeout = '30s'")

	// Get all columns — simple query without PK join for speed on pooled connections
	rows, err := db.Query(`
		SELECT
			table_name,
			column_name,
			data_type,
			is_nullable,
			COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = 'public'
		ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		return nil, fmt.Errorf("querying schema: %w", err)
	}
	defer rows.Close()

	tableMap := make(map[string]*Table)
	var tableOrder []string

	for rows.Next() {
		var tableName, colName, dataType, isNullable, colDefault string

		if err := rows.Scan(&tableName, &colName, &dataType, &isNullable, &colDefault); err != nil {
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
