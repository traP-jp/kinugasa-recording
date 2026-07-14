package livekit

import (
	"strings"
	"testing"
	"time"

	"github.com/livekit/protocol/auth"
)

func TestTokenIssuerCreatesShortLivedSubscribeOnlyToken(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	issuer := &TokenIssuer{APIKey: "key", APISecret: "secret", ServerURL: "wss://livekit.example", RoomName: "kinugasa-preview", TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	issued, err := issuer.Issue()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.ParseAPIToken(issued.ParticipantToken)
	if err != nil {
		t.Fatal(err)
	}
	_, claims, err := verifier.Verify("secret")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Video == nil || !claims.Video.RoomJoin || claims.Video.Room != "kinugasa-preview" || claims.Video.GetCanPublish() || !claims.Video.GetCanSubscribe() || claims.Video.GetCanPublishData() {
		t.Fatalf("video grant = %#v", claims.Video)
	}
	if !strings.HasPrefix(claims.Identity, "preview-") || issued.ExpiresAt != now.Add(5*time.Minute) {
		t.Fatalf("issued token = %#v, identity = %q", issued, claims.Identity)
	}
}

func TestTokenIssuerRejectsExcessiveTTL(t *testing.T) {
	t.Parallel()
	issuer := &TokenIssuer{APIKey: "key", APISecret: "secret", ServerURL: "url", RoomName: "room", TTL: time.Hour}
	if _, err := issuer.Issue(); err == nil {
		t.Fatal("Issue() accepted excessive TTL")
	}
}
