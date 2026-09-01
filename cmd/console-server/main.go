package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	workerv1 "github.com/traP-jp/kinugasa-recording/gen/console_video_worker/v1"
	"github.com/traP-jp/kinugasa-recording/internal/console/api"
	"github.com/traP-jp/kinugasa-recording/internal/console/application"
	"github.com/traP-jp/kinugasa-recording/internal/console/config"
	"github.com/traP-jp/kinugasa-recording/internal/console/preview"
	"github.com/traP-jp/kinugasa-recording/internal/console/repository/postgres"
	"github.com/traP-jp/kinugasa-recording/internal/console/workercontrol"
	livekitingress "github.com/traP-jp/kinugasa-recording/internal/livekit/ingress"
	"github.com/traP-jp/kinugasa-recording/internal/operator"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("console server stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	serverConfig, err := config.FromEnvironment()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	pool, err := pgxpool.New(ctx, serverConfig.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}
	previewIssuer, err := preview.NewIssuer(serverConfig.LiveKitAPIKey, serverConfig.LiveKitSecret)
	if err != nil {
		return fmt.Errorf("configure preview tokens: %w", err)
	}
	previewIngress, err := livekitingress.NewClient(
		serverConfig.LiveKitURL, serverConfig.LiveKitAPIKey, serverConfig.LiveKitSecret, nil,
	)
	if err != nil {
		return fmt.Errorf("configure LiveKit ingress: %w", err)
	}
	repository := postgres.New(pool)
	grpcListener, err := net.Listen("tcp", serverConfig.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen for worker gRPC: %w", err)
	}
	defer func() { _ = grpcListener.Close() }()
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(4<<20),
		grpc.MaxSendMsgSize(4<<20),
	)
	workerRegistry := workercontrol.NewRegistry()
	workerv1.RegisterConsoleVideoWorkerServiceServer(
		grpcServer,
		workercontrol.NewServer(repository, workerRegistry),
	)
	operatorConfig, err := config.OperatorFromEnvironment()
	if err != nil {
		return fmt.Errorf("load operator configuration: %w", err)
	}
	var operatorManager ctrl.Manager
	if operatorConfig.Enabled {
		kubeConfig, err := ctrl.GetConfig()
		if err != nil {
			return fmt.Errorf("load Kubernetes configuration: %w", err)
		}
		operatorManager, err = operator.NewManager(kubeConfig, operatorConfig.Manager, repository, previewIngress, logger)
		if err != nil {
			return err
		}
	}

	service := application.New(repository).
		WithCommandDispatcher(workerRegistry).
		WithObjectBucket(serverConfig.ObjectBucket).
		WithPreviewAccess(serverConfig.LiveKitURL, serverConfig.PreviewTTL, previewIssuer)
	server := &http.Server{
		Addr:              serverConfig.ListenAddress,
		Handler:           api.NewHandler(service, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	serveError := make(chan error, 1)
	go func() {
		logger.Info("console server listening", "address", serverConfig.ListenAddress)
		serveError <- server.ListenAndServe()
	}()
	grpcServeError := make(chan error, 1)
	go func() {
		logger.Info("worker control gRPC listening", "address", serverConfig.GRPCAddress)
		grpcServeError <- grpcServer.Serve(grpcListener)
	}()
	var operatorError <-chan error
	if operatorManager != nil {
		operatorErrors := make(chan error, 1)
		operatorError = operatorErrors
		go func() {
			logger.Info("Kubernetes operator starting", "namespace", operatorConfig.Manager.Namespace)
			operatorErrors <- operatorManager.Start(runtimeContext)
		}()
	}

	var componentError error
	select {
	case err := <-serveError:
		if !errors.Is(err, http.ErrServerClosed) {
			componentError = fmt.Errorf("serve HTTP: %w", err)
		}
	case err := <-grpcServeError:
		if !errors.Is(err, grpc.ErrServerStopped) {
			componentError = fmt.Errorf("serve worker gRPC: %w", err)
		}
	case err := <-operatorError:
		if err != nil {
			componentError = fmt.Errorf("run Kubernetes operator: %w", err)
		}
	case <-ctx.Done():
	}
	cancelRuntime()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), serverConfig.ShutdownWait)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownContext); err != nil {
		componentError = errors.Join(componentError, fmt.Errorf("shut down HTTP server: %w", err))
	}
	if err := stopGRPC(shutdownContext, grpcServer); err != nil {
		componentError = errors.Join(componentError, err)
	}
	return componentError
}
