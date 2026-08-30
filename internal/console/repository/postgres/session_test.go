package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

func TestSessionRepository(t *testing.T) {
	pool := resetDatabase(t)
	store := New(pool)
	ctx := context.Background()
	createdAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	sessions := []domain.Session{
		{ID: "019c240d-a6de-7de0-a826-0f26e8803fc0", Name: "older", State: domain.SessionStateActive, CreatedAt: createdAt.Add(-time.Hour)},
		{ID: "019c240e-3eb4-72d6-a6fa-adfe1df795c8", Name: "beta", State: domain.SessionStateActive, CreatedAt: createdAt},
		{ID: "019c240e-4a04-73e3-8328-a32a246b8c47", Name: "alpha", State: domain.SessionStateInactive, CreatedAt: createdAt},
	}
	for _, session := range sessions {
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession(%q) error = %v", session.Name, err)
		}
	}

	page, err := store.ListSessions(ctx, repository.PageRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("ListSessions() total = %d, want 3", page.Total)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "alpha" || page.Items[1].Name != "beta" {
		t.Fatalf("ListSessions() names = %v, want [alpha beta]", sessionNames(page.Items))
	}

	emptyPage, err := store.ListSessions(ctx, repository.PageRequest{Page: 3, PageSize: 2})
	if err != nil {
		t.Fatalf("ListSessions(empty page) error = %v", err)
	}
	if emptyPage.Total != 3 || len(emptyPage.Items) != 0 {
		t.Fatalf("empty page = %+v, want total 3 and no items", emptyPage)
	}

	detail, err := store.GetSession(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if detail.Session.ID != sessions[2].ID || detail.OngoingTakeName != "" {
		t.Fatalf("GetSession() = %+v", detail)
	}

	if err := store.CreateSession(ctx, domain.Session{
		ID:        "019c240e-5141-75e4-8b4b-5c611e7fab65",
		Name:      "alpha",
		State:     domain.SessionStateActive,
		CreatedAt: createdAt,
	}); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("duplicate CreateSession() error = %v, want ErrConflict", err)
	}
	if _, err := store.GetSession(ctx, "missing"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetSession(missing) error = %v, want ErrNotFound", err)
	}
}

func sessionNames(sessions []domain.Session) []string {
	names := make([]string, len(sessions))
	for index, session := range sessions {
		names[index] = session.Name
	}
	return names
}
