package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

const sessionReservationFile = ".kinugasa-session"

// ErrNameReserved indicates that a session name is currently or historically used.
var ErrNameReserved = errors.New("session name is reserved")

type s3SessionAPI interface {
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3SessionRegistry reserves session names in an S3-compatible bucket.
type S3SessionRegistry struct {
	client s3SessionAPI
	bucket string
}

// NewS3SessionRegistry creates a session name registry backed by S3.
func NewS3SessionRegistry(client s3SessionAPI, bucket string) *S3SessionRegistry {
	return &S3SessionRegistry{client: client, bucket: bucket}
}

// Reserve rejects an existing prefix and atomically creates the reservation object.
func (registry *S3SessionRegistry) Reserve(ctx context.Context, name string) error {
	prefix := name + "/"
	listed, err := registry.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(registry.bucket),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(1),
	})
	if err != nil {
		return fmt.Errorf("list session prefix: %w", err)
	}
	if len(listed.Contents) > 0 {
		return ErrNameReserved
	}

	_, err = registry.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(registry.bucket),
		Key:         aws.String(prefix + sessionReservationFile),
		Body:        strings.NewReader("{\"version\":1}\n"),
		ContentType: aws.String("application/json"),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		var apiError smithy.APIError
		if errors.As(err, &apiError) && (apiError.ErrorCode() == "PreconditionFailed" || apiError.ErrorCode() == "ConditionalRequestConflict") {
			return ErrNameReserved
		}

		return fmt.Errorf("create session reservation: %w", err)
	}

	return nil
}
