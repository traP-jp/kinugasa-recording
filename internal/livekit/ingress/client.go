package ingress

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tokenLifetime = 5 * time.Minute

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	baseURL   *url.URL
	apiKey    string
	apiSecret string
	http      HTTPClient
	now       func() time.Time
}

type Endpoint struct {
	IngressID string
	URL       string
	StreamKey string
}

func NewClient(liveKitURL, apiKey, apiSecret string, httpClient HTTPClient) (*Client, error) {
	baseURL, err := url.Parse(liveKitURL)
	if err != nil || baseURL.Host == "" {
		return nil, fmt.Errorf("LiveKit URL must be absolute")
	}
	switch baseURL.Scheme {
	case "ws":
		baseURL.Scheme = "http"
	case "wss":
		baseURL.Scheme = "https"
	case "http", "https":
	default:
		return nil, fmt.Errorf("LiveKit URL must use ws, wss, http, or https")
	}
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(apiSecret) == "" {
		return nil, fmt.Errorf("LiveKit API key and secret must be set")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, apiSecret: apiSecret, http: httpClient, now: time.Now}, nil
}

func (c *Client) Create(ctx context.Context, room, participantIdentity, name string) (Endpoint, error) {
	if room == "" || participantIdentity == "" || name == "" {
		return Endpoint{}, fmt.Errorf("room, participant identity, and ingress name must be set")
	}
	request := createRequest{
		InputType:           1,
		Name:                name,
		RoomName:            room,
		ParticipantIdentity: participantIdentity,
		ParticipantName:     participantIdentity,
		EnableTranscoding:   false,
	}
	var response ingressResponse
	if err := c.call(ctx, "CreateIngress", request, &response); err != nil {
		return Endpoint{}, err
	}
	endpoint := response.endpoint()
	if endpoint.IngressID == "" || endpoint.URL == "" || endpoint.StreamKey == "" {
		return Endpoint{}, fmt.Errorf("LiveKit CreateIngress returned incomplete connection settings")
	}
	return endpoint, nil
}

func (c *Client) Delete(ctx context.Context, ingressID string) error {
	if ingressID == "" {
		return fmt.Errorf("ingress ID must be set")
	}
	var response ingressResponse
	err := c.call(ctx, "DeleteIngress", deleteRequest{IngressID: ingressID}, &response)
	if apiError, ok := err.(*APIError); ok && apiError.Code == "not_found" {
		return nil
	}
	return err
}

func (c *Client) call(ctx context.Context, method string, body, response any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode LiveKit %s request: %w", method, err)
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/twirp/livekit.Ingress/" + method})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("create LiveKit %s request: %w", method, err)
	}
	token, err := c.adminToken()
	if err != nil {
		return fmt.Errorf("sign LiveKit %s request: %w", method, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	httpResponse, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call LiveKit %s: %w", method, err)
	}
	defer func() { _ = httpResponse.Body.Close() }()
	limited := io.LimitReader(httpResponse.Body, 1<<20)
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		var failure APIError
		if decodeError := json.NewDecoder(limited).Decode(&failure); decodeError != nil {
			return fmt.Errorf("LiveKit %s returned HTTP %d", method, httpResponse.StatusCode)
		}
		failure.Status = httpResponse.StatusCode
		return &failure
	}
	if err := json.NewDecoder(limited).Decode(response); err != nil {
		return fmt.Errorf("decode LiveKit %s response: %w", method, err)
	}
	return nil
}

func (c *Client) adminToken() (string, error) {
	now := c.now().UTC()
	claims := adminClaims{
		Issuer: c.apiKey, Subject: c.apiKey, IssuedAt: now.Unix(), NotBefore: now.Unix(),
		ExpiresAt: now.Add(tokenLifetime).Unix(), Video: adminGrant{IngressAdmin: true},
	}
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
	signature := hmac.New(sha256.New, []byte(c.apiSecret))
	_, _ = signature.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil)), nil
}

type createRequest struct {
	InputType           int    `json:"inputType"`
	Name                string `json:"name"`
	RoomName            string `json:"roomName"`
	ParticipantIdentity string `json:"participantIdentity"`
	ParticipantName     string `json:"participantName"`
	EnableTranscoding   bool   `json:"enableTranscoding"`
}

type deleteRequest struct {
	IngressID string `json:"ingressId"`
}

type ingressResponse struct {
	IngressID      string `json:"ingressId"`
	IngressIDProto string `json:"ingress_id"`
	URL            string `json:"url"`
	StreamKey      string `json:"streamKey"`
	StreamKeyProto string `json:"stream_key"`
}

func (r ingressResponse) endpoint() Endpoint {
	ingressID := r.IngressID
	if ingressID == "" {
		ingressID = r.IngressIDProto
	}
	streamKey := r.StreamKey
	if streamKey == "" {
		streamKey = r.StreamKeyProto
	}
	return Endpoint{IngressID: ingressID, URL: r.URL, StreamKey: streamKey}
}

type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"msg"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("LiveKit API returned HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

type adminClaims struct {
	Issuer    string     `json:"iss"`
	Subject   string     `json:"sub"`
	IssuedAt  int64      `json:"iat"`
	NotBefore int64      `json:"nbf"`
	ExpiresAt int64      `json:"exp"`
	Video     adminGrant `json:"video"`
}

type adminGrant struct {
	IngressAdmin bool `json:"ingressAdmin"`
}
