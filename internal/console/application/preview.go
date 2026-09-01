package application

import (
	"context"
	"fmt"
	"time"

	"github.com/traP-jp/kinugasa-recording/internal/console/domain"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository"
)

type PreviewTokenIssuer interface {
	Issue(room, identity string, validFor time.Duration) (string, error)
}

type PreviewAccess struct {
	URL         string
	AccessToken string
	ExpiresAt   time.Time
}

func (s *Service) WithPreviewAccess(url string, tokenTTL time.Duration, issuer PreviewTokenIssuer) *Service {
	s.previewURL = url
	s.previewTokenTTL = tokenTTL
	s.previewIssuer = issuer
	return s
}

func (s *Service) CreatePreviewAccess(ctx context.Context, sessionName string) (PreviewAccess, error) {
	session, err := s.repository.GetSession(ctx, sessionName)
	if err != nil {
		return PreviewAccess{}, err
	}
	if session.Session.State != domain.SessionStateActive {
		return PreviewAccess{}, repository.ErrConflict
	}
	if s.previewURL == "" || s.previewTokenTTL <= 0 || s.previewIssuer == nil {
		return PreviewAccess{}, fmt.Errorf("preview access is not configured")
	}
	identity, err := s.newID()
	if err != nil {
		return PreviewAccess{}, fmt.Errorf("generate preview identity: %w", err)
	}
	token, err := s.previewIssuer.Issue(sessionName, "preview-"+identity, s.previewTokenTTL)
	if err != nil {
		return PreviewAccess{}, fmt.Errorf("issue preview token: %w", err)
	}
	return PreviewAccess{
		URL: s.previewURL, AccessToken: token, ExpiresAt: s.now().UTC().Add(s.previewTokenTTL),
	}, nil
}
