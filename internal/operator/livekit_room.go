package operator

import (
	"context"
	"fmt"
	"time"

	livekit "github.com/livekit/protocol/livekit"
)

type LiveKitRoomAPI interface {
	ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error)
	CreateRoom(context.Context, *livekit.CreateRoomRequest) (*livekit.Room, error)
}

type LiveKitRoomInitializer struct {
	API           LiveKitRoomAPI
	RoomName      string
	RetryInterval time.Duration
}

func (initializer *LiveKitRoomInitializer) Start(ctx context.Context) error {
	interval := initializer.RetryInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := initializer.Ensure(ctx); err == nil {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (initializer *LiveKitRoomInitializer) Ensure(ctx context.Context) error {
	if initializer.API == nil || initializer.RoomName == "" {
		return fmt.Errorf("LiveKit room initializer is not configured")
	}
	rooms, err := initializer.API.ListRooms(ctx, &livekit.ListRoomsRequest{Names: []string{initializer.RoomName}})
	if err != nil {
		return fmt.Errorf("list LiveKit rooms: %w", err)
	}
	for _, room := range rooms.Rooms {
		if room.Name == initializer.RoomName {
			return nil
		}
	}
	if _, err := initializer.API.CreateRoom(ctx, &livekit.CreateRoomRequest{Name: initializer.RoomName}); err != nil {
		return fmt.Errorf("create LiveKit preview room: %w", err)
	}
	return nil
}

func (*LiveKitRoomInitializer) NeedLeaderElection() bool { return true }
