package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("validate session: %w", err)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, name, state, created_at)
		VALUES ($1, $2, $3, $4)`,
		session.ID, session.Name, session.State, session.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", classifyError(err))
	}
	return nil
}

func (s *Store) ListSessions(ctx context.Context, page repository.PageRequest) (repository.SessionPage, error) {
	result := repository.SessionPage{Items: make([]domain.Session, 0)}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&result.Total); err != nil {
		return repository.SessionPage{}, fmt.Errorf("count sessions: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, name, state, created_at
		FROM sessions
		ORDER BY created_at DESC, name ASC
		LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return repository.SessionPage{}, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var session domain.Session
		if err := rows.Scan(&session.ID, &session.Name, &session.State, &session.CreatedAt); err != nil {
			return repository.SessionPage{}, fmt.Errorf("scan session: %w", err)
		}
		result.Items = append(result.Items, session)
	}
	if err := rows.Err(); err != nil {
		return repository.SessionPage{}, fmt.Errorf("iterate sessions: %w", err)
	}
	return result, nil
}

func (s *Store) GetSession(ctx context.Context, name string) (repository.SessionDetail, error) {
	var result repository.SessionDetail
	err := s.pool.QueryRow(ctx, `
		SELECT sessions.id, sessions.name, sessions.state, sessions.created_at,
		       coalesce(takes.name, '')
		FROM sessions
		LEFT JOIN takes ON takes.session_id = sessions.id AND takes.phase = 'ongoing'
		WHERE sessions.name = $1`, name,
	).Scan(
		&result.Session.ID,
		&result.Session.Name,
		&result.Session.State,
		&result.Session.CreatedAt,
		&result.OngoingTakeName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.SessionDetail{}, repository.ErrNotFound
	}
	if err != nil {
		return repository.SessionDetail{}, fmt.Errorf("get session: %w", err)
	}
	return result, nil
}
