package operator

import (
	"context"
	"testing"

	livekit "github.com/livekit/protocol/livekit"
)

type roomAPIStub struct {
	rooms   []*livekit.Room
	creates int
}

func (stub *roomAPIStub) ListRooms(context.Context, *livekit.ListRoomsRequest) (*livekit.ListRoomsResponse, error) {
	return &livekit.ListRoomsResponse{Rooms: stub.rooms}, nil
}
func (stub *roomAPIStub) CreateRoom(_ context.Context, request *livekit.CreateRoomRequest) (*livekit.Room, error) {
	stub.creates++
	room := &livekit.Room{Name: request.Name}
	stub.rooms = append(stub.rooms, room)
	return room, nil
}

func TestLiveKitRoomInitializerIsIdempotent(t *testing.T) {
	t.Parallel()
	api := &roomAPIStub{}
	initializer := &LiveKitRoomInitializer{API: api, RoomName: "kinugasa-preview"}
	if err := initializer.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := initializer.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.creates != 1 {
		t.Fatalf("CreateRoom calls = %d", api.creates)
	}
}
