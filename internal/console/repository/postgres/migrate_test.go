package postgres

import (
	"context"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	pool := resetDatabase(t)

	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
	var migrations int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatalf("count schema migrations: %v", err)
	}
	if migrations != 3 {
		t.Fatalf("migration count = %d, want 3", migrations)
	}
}
