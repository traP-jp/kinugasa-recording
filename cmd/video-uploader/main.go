package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/comavius/kinugasa-recording/internal/media"
	storagelib "github.com/comavius/kinugasa-recording/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Printf("video-uploader: %v", err)
		os.Exit(1)
	}
}

func run() error {
	environment := media.Environment{}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	region := environment.String("S3_REGION", "us-east-1")
	configuration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return err
	}
	usePathStyle, err := strconv.ParseBool(environment.String("S3_USE_PATH_STYLE", "false"))
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(configuration, func(options *s3.Options) {
		options.UsePathStyle = usePathStyle
		if endpoint := os.Getenv("S3_ENDPOINT"); endpoint != "" {
			options.BaseEndpoint = aws.String(endpoint)
		}
	})
	bucket, err := environment.Required("S3_BUCKET")
	if err != nil {
		return err
	}
	session, err := environment.Required("SESSION_NAME")
	if err != nil {
		return err
	}
	take, err := environment.Required("TAKE_NAME")
	if err != nil {
		return err
	}
	camera, err := environment.Required("CAMERA_NAME")
	if err != nil {
		return err
	}
	uploader, err := storagelib.NewUploader(client, storagelib.UploadConfig{
		Root: environment.String("RECORDING_ROOT", "/recording"), Bucket: bucket,
		Session: session, Take: take, Camera: camera,
	})
	if err != nil {
		return err
	}
	return runUploader(ctx, uploader, environment.String("STATUS_ADDRESS", ":8080"))
}

func runUploader(ctx context.Context, uploader *storagelib.Uploader, statusAddress string) error {
	if statusAddress == "" {
		return uploader.Run(ctx)
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Addr: statusAddress, Handler: uploaderStatusHandler(uploader), ReadHeaderTimeout: 5 * time.Second}
	uploadErrors := make(chan error, 1)
	serverErrors := make(chan error, 1)
	go func() { uploadErrors <- uploader.Run(runContext) }()
	go func() { serverErrors <- server.ListenAndServe() }()

	var runError error
	select {
	case runError = <-uploadErrors:
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runError = err
		}
	}
	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownContext)
	return runError
}

func uploaderStatusHandler(uploader *storagelib.Uploader) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) {
		phase := uploader.Snapshot().Phase
		if phase == "Failed" {
			http.Error(response, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/status", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(map[string]any{"uploader": uploader.Snapshot()})
	})
	return mux
}
