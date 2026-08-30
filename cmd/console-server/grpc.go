package main

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
)

func stopGRPC(ctx context.Context, server *grpc.Server) error {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		server.Stop()
		<-stopped
		return fmt.Errorf("graceful gRPC shutdown: %w", ctx.Err())
	}
}
