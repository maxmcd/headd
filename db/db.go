package db

import (
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var embedFS embed.FS

type DB struct {
	db *sql.DB
}

type Host struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Build struct {
	ID          string            `json:"id"`
	HostID      string            `json:"host_id"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Status      string            `json:"status"`
	Output      string            `json:"output"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at"`
}

func New(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}

	// Create migrations table if it doesn't exist - stores single row with highest migration
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS migrations (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL
		) STRICT;
	`)
	if err != nil {
		return nil, fmt.Errorf("creating migrations table: %w", err)
	}

	// Get current migration version, initialize to 0 if not exists
	var version int
	err = db.QueryRow("SELECT version FROM migrations WHERE id = 1").Scan(&version)
	if err == sql.ErrNoRows {
		_, err = db.Exec("INSERT INTO migrations (id, version) VALUES (1, 0)")
		if err != nil {
			return nil, fmt.Errorf("initializing migrations: %w", err)
		}
		version = 0
	} else if err != nil {
		return nil, fmt.Errorf("querying migration version: %w", err)
	}

	// Read and sort migration files
	entries, err := embedFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("reading migrations dir: %w", err)
	}

	var migrations []struct {
		filename string
		version  int
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			numStr := strings.TrimPrefix(entry.Name(), "000")
			numStr = strings.TrimSuffix(numStr, ".sql")
			num, err := strconv.Atoi(numStr)
			if err != nil {
				return nil, fmt.Errorf("invalid migration filename %s: %w", entry.Name(), err)
			}
			migrations = append(migrations, struct {
				filename string
				version  int
			}{entry.Name(), num})
		}
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	// Apply new migrations in order
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}

		content, err := embedFS.ReadFile(filepath.Join("migrations", migration.filename))
		if err != nil {
			return nil, fmt.Errorf("reading migration %s: %w", migration.filename, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return nil, fmt.Errorf("beginning transaction: %w", err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("applying migration %s: %w", migration.filename, err)
		}

		if _, err := tx.Exec("UPDATE migrations SET version = ? WHERE id = 1", migration.version); err != nil {
			_ = tx.Rollback()
			return nil, fmt.Errorf("updating migration version to %d: %w", migration.version, err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("committing migration %s: %w", migration.filename, err)
		}
	}

	return &DB{db: db}, nil
}

func (db *DB) Close() error {
	return db.db.Close()
}
