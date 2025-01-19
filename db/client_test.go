package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestClient(t *testing.T) {
	db, err := sql.Open("libsql", "file:test.db")
	if err != nil {
		t.Fatal(err)
	}
	client := New(db)
	ctx := context.Background()
	_, _ = client.InsertApp(ctx, InsertAppParams{
		Name: "test",
		User: "test",
		Meta: "test",
	})
}
