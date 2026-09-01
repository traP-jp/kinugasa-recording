package preview

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIssuerCreatesSubscribeOnlyRoomToken(t *testing.T) {
	issuer, err := NewIssuer("api-key", "api-secret")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC)
	issuer.now = func() time.Time { return now }
	token, err := issuer.Issue("session-1", "preview-id", 5*time.Minute)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT parts = %d", len(parts))
	}
	signature := hmac.New(sha256.New, []byte("api-secret"))
	_, _ = signature.Write([]byte(parts[0] + "." + parts[1]))
	wantSignature := base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(wantSignature)) {
		t.Fatal("JWT signature does not match")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode JWT claims: %v", err)
	}
	if claims.Issuer != "api-key" || claims.Subject != "preview-id" || claims.Identity != "preview-id" ||
		!claims.Video.RoomJoin || claims.Video.Room != "session-1" || claims.Video.CanPublish ||
		claims.Video.CanPublishData || !claims.Video.CanSubscribe || claims.ExpiresAt-claims.IssuedAt != 300 {
		t.Fatalf("token claims = %+v", claims)
	}
}
