package db

import (
	"testing"
)

func TestOpenDB(t *testing.T) {
	db, err := New("db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer db.db.Close()

	// Query schema for all tables h
	rows, err := db.db.Query(`
		SELECT name, sql
		FROM sqlite_master
		WHERE type='table'
		ORDER BY name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, schema string
		if err := rows.Scan(&name, &schema); err != nil {
			t.Fatal(err)
		}
		t.Logf("Table %s schema:\n%s\n", name, schema)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
