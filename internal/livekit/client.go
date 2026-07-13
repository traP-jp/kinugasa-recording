package livekit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/livekit/protocol/auth"
	livekit "github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
)

type Client struct {
	ingress           livekit.Ingress
	rooms             livekit.RoomService
	apiKey, apiSecret string
}

func NewClient(serverURL, apiKey, apiSecret string) (*Client, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parse LiveKit URL: %w", err)
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("LiveKit URL must use http(s) or ws(s)")
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}
	return &Client{
		ingress: livekit.NewIngressProtobufClient(parsed.String(), httpClient),
		rooms:   livekit.NewRoomServiceProtobufClient(parsed.String(), httpClient),
		apiKey:  apiKey, apiSecret: apiSecret,
	}, nil
}

func (client *Client) CreateIngress(ctx context.Context, request *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{IngressAdmin: true})
	if err != nil {
		return nil, err
	}
	return client.ingress.CreateIngress(ctx, request)
}
func (client *Client) ListIngress(ctx context.Context, request *livekit.ListIngressRequest) (*livekit.ListIngressResponse, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{IngressAdmin: true})
	if err != nil {
		return nil, err
	}
	return client.ingress.ListIngress(ctx, request)
}
func (client *Client) DeleteIngress(ctx context.Context, request *livekit.DeleteIngressRequest) (*livekit.IngressInfo, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{IngressAdmin: true})
	if err != nil {
		return nil, err
	}
	return client.ingress.DeleteIngress(ctx, request)
}
func (client *Client) ListRooms(ctx context.Context, request *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{RoomList: true})
	if err != nil {
		return nil, err
	}
	return client.rooms.ListRooms(ctx, request)
}
func (client *Client) CreateRoom(ctx context.Context, request *livekit.CreateRoomRequest) (*livekit.Room, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{RoomCreate: true})
	if err != nil {
		return nil, err
	}
	return client.rooms.CreateRoom(ctx, request)
}

func (client *Client) ListParticipants(ctx context.Context, request *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error) {
	ctx, err := client.authorize(ctx, &auth.VideoGrant{RoomAdmin: true, Room: request.Room})
	if err != nil {
		return nil, err
	}
	return client.rooms.ListParticipants(ctx, request)
}

func (client *Client) authorize(ctx context.Context, grant *auth.VideoGrant) (context.Context, error) {
	token, err := auth.NewAccessToken(client.apiKey, client.apiSecret).SetVideoGrant(grant).ToJWT()
	if err != nil {
		return nil, fmt.Errorf("create LiveKit API token: %w", err)
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	return twirp.WithHTTPRequestHeaders(ctx, headers)
}
