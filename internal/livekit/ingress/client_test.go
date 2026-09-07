package ingress

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientCreatesChecksAndDeletesWHIPIngress(t *testing.T) {
	methods := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		methods <- request.URL.Path
		assertAdminToken(t, request.Header.Get("Authorization"))
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/twirp/livekit.Ingress/CreateIngress":
			var body createRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if body.InputType != 1 || body.RoomName != "session-1" || body.ParticipantIdentity != "camera-1" || body.EnableTranscoding {
				t.Errorf("create request = %+v", body)
			}
			_, _ = response.Write([]byte(`{"ingressId":"IN_1","url":"https://ingress.example.com/whip","streamKey":"stream-key"}`))
		case "/twirp/livekit.Ingress/ListIngress":
			var body listIngressRequest
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode list request: %v", err)
			}
			if body.IngressID != "IN_1" {
				t.Errorf("list request = %+v", body)
			}
			_, _ = response.Write([]byte(`{"items":[{"ingress_id":"IN_1","url":"https://ingress.example.com/whip","stream_key":"stream-key"}]}`))
		case "/twirp/livekit.Ingress/DeleteIngress":
			_, _ = response.Write([]byte(`{"ingressId":"IN_1"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "api-key", "api-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) }
	endpoint, err := client.Create(context.Background(), "session-1", "camera-1", "session-1-camera-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if endpoint.IngressID != "IN_1" || endpoint.StreamKey != "stream-key" {
		t.Fatalf("Create() = %+v", endpoint)
	}
	exists, err := client.Exists(context.Background(), endpoint.IngressID)
	if err != nil || !exists {
		t.Fatalf("Exists() = %t, %v", exists, err)
	}
	if err := client.Delete(context.Background(), endpoint.IngressID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	close(methods)
	if len(methods) != 3 {
		t.Fatalf("API call count = %d", len(methods))
	}
}

func TestClientTreatsMissingIngressAsDeletedAndAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"code":"not_found","msg":"missing"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "api-key", "api-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(context.Background(), "IN_missing"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err := client.Exists(context.Background(), "IN_missing")
	if err != nil || exists {
		t.Fatalf("Exists() = %t, %v", exists, err)
	}
}

func assertAdminToken(t *testing.T, authorization string) {
	t.Helper()
	token := strings.TrimPrefix(authorization, "Bearer ")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("authorization = %q", authorization)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims adminClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "api-key" || !claims.Video.IngressAdmin || claims.ExpiresAt-claims.IssuedAt != 300 {
		t.Fatalf("admin claims = %+v", claims)
	}
}
