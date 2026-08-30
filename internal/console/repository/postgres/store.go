package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func classifyError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23505":
			return errors.Join(repository.ErrConflict, err)
		}
	}
	return err
}

func classifyMissingParent(err error, constraint string) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23503" && postgresError.ConstraintName == constraint {
		return errors.Join(repository.ErrNotFound, err)
	}
	return classifyError(err)
}
