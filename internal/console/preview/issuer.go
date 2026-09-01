package preview

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Issuer struct {
	apiKey    string
	apiSecret string
	now       func() time.Time
}

func NewIssuer(apiKey, apiSecret string) (*Issuer, error) {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(apiSecret) == "" {
		return nil, fmt.Errorf("LiveKit API key and secret must be set")
	}
	return &Issuer{apiKey: apiKey, apiSecret: apiSecret, now: time.Now}, nil
}

func (i *Issuer) Issue(room, identity string, validFor time.Duration) (string, error) {
	if room == "" || identity == "" || validFor <= 0 {
		return "", fmt.Errorf("LiveKit room, identity, and positive token lifetime must be set")
	}
	now := i.now().UTC()
	claims := tokenClaims{
		Issuer: i.apiKey, Subject: identity, IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: now.Add(validFor).Unix(),
		Identity: identity,
		Video: videoGrant{
			RoomJoin: true, Room: room, CanPublish: false, CanSubscribe: true, CanPublishData: false,
		},
	}
	return signJWT(claims, i.apiSecret)
}

type tokenClaims struct {
	Issuer    string     `json:"iss"`
	Subject   string     `json:"sub"`
	IssuedAt  int64      `json:"iat"`
	NotBefore int64      `json:"nbf"`
	ExpiresAt int64      `json:"exp"`
	Identity  string     `json:"identity"`
	Video     videoGrant `json:"video"`
}

type videoGrant struct {
	RoomJoin       bool   `json:"roomJoin"`
	Room           string `json:"room"`
	CanPublish     bool   `json:"canPublish"`
	CanSubscribe   bool   `json:"canSubscribe"`
	CanPublishData bool   `json:"canPublishData"`
}

func signJWT(claims tokenClaims, secret string) (string, error) {
	header, err := json.Marshal(struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}{Algorithm: "HS256", Type: "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil)), nil
}
