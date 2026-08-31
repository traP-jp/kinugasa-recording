package s3store

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Config struct {
	Bucket       string
	Region       string
	Endpoint     string
	UsePathStyle bool
}

type putObjectClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type Store struct {
	client putObjectClient
	bucket string
}

func New(ctx context.Context, config Config) (*Store, error) {
	if strings.TrimSpace(config.Bucket) == "" || strings.TrimSpace(config.Region) == "" {
		return nil, fmt.Errorf("S3 bucket and region must be set")
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(config.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	client := s3.NewFromConfig(awsConfiguration, func(options *s3.Options) {
		options.UsePathStyle = config.UsePathStyle
		if config.Endpoint != "" {
			options.BaseEndpoint = aws.String(config.Endpoint)
		}
	})
	return &Store{client: client, bucket: config.Bucket}, nil
}

func (s *Store) Put(
	ctx context.Context,
	key string,
	body io.ReadSeeker,
	size int64,
	digest string,
	mediaType string,
) error {
	digestBytes, err := hex.DecodeString(digest)
	if err != nil || len(digestBytes) != 32 {
		return fmt.Errorf("object SHA-256 must be 64 hexadecimal characters")
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:         aws.String(s.bucket),
		Key:            aws.String(key),
		Body:           body,
		ContentLength:  aws.Int64(size),
		ContentType:    aws.String(mediaType),
		ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(digestBytes)),
		Metadata:       map[string]string{"sha256": digest},
	})
	if err != nil {
		return fmt.Errorf("put S3 object %q: %w", key, err)
	}
	return nil
}
