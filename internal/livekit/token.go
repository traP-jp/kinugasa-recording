package livekit

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/livekit/protocol/auth"
)

const MaximumPreviewTokenTTL = 15 * time.Minute

type PreviewToken struct {
	ServerURL, RoomName, ParticipantToken string
	ExpiresAt                             time.Time
}

type TokenIssuer struct {
	APIKey, APISecret, ServerURL, RoomName string
	TTL                                    time.Duration
	Now                                    func() time.Time
}

func (issuer *TokenIssuer) Issue() (*PreviewToken, error) {
	if issuer.APIKey == "" || issuer.APISecret == "" || issuer.ServerURL == "" || issuer.RoomName == "" {
		return nil, fmt.Errorf("LiveKit token issuer is not configured")
	}
	ttl := issuer.TTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	if ttl < time.Minute || ttl > MaximumPreviewTokenTTL {
		return nil, fmt.Errorf("preview token TTL must be between 1m and %s", MaximumPreviewTokenTTL)
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return nil, fmt.Errorf("generate participant identity: %w", err)
	}
	publish, subscribe, publishData, updateMetadata := false, true, false, false
	grant := &auth.VideoGrant{
		RoomJoin: true, Room: issuer.RoomName, CanPublish: &publish, CanSubscribe: &subscribe,
		CanPublishData: &publishData, CanUpdateOwnMetadata: &updateMetadata,
	}
	token, err := auth.NewAccessToken(issuer.APIKey, issuer.APISecret).
		SetIdentity("preview-" + hex.EncodeToString(identifier)).SetValidFor(ttl).SetVideoGrant(grant).ToJWT()
	if err != nil {
		return nil, fmt.Errorf("sign preview token: %w", err)
	}
	now := time.Now
	if issuer.Now != nil {
		now = issuer.Now
	}
	return &PreviewToken{ServerURL: issuer.ServerURL, RoomName: issuer.RoomName, ParticipantToken: token, ExpiresAt: now().UTC().Add(ttl)}, nil
}
