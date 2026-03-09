package schema

import (
	"database/sql"
	"encoding/json"
)

// SaveToStore persists the schema to the cleancode SQLite index.
func SaveToStore(db *sql.DB, schema *DBSchema) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS db_schema (
			table_name TEXT PRIMARY KEY,
			columns_json TEXT NOT NULL
		)
	`)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM db_schema")

	stmt, err := tx.Prepare("INSERT INTO db_schema (table_name, columns_json) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, table := range schema.Tables {
		colJSON, err := json.Marshal(table.Columns)
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(table.Name, string(colJSON)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LoadFromStore loads the schema from the cleancode SQLite index.
func LoadFromStore(db *sql.DB) (*DBSchema, error) {
	// Check if the table exists
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='db_schema'").Scan(&count)
	if err != nil || count == 0 {
		return nil, nil // No schema stored
	}

	rows, err := db.Query("SELECT table_name, columns_json FROM db_schema ORDER BY table_name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var name, colJSON string
		if err := rows.Scan(&name, &colJSON); err != nil {
			return nil, err
		}
		var columns []Column
		if err := json.Unmarshal([]byte(colJSON), &columns); err != nil {
			return nil, err
		}
		tables = append(tables, Table{Name: name, Columns: columns})
	}

	if len(tables) == 0 {
		return nil, nil
	}

	return &DBSchema{Tables: tables}, nil
}
