package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestS3SessionRegistryReservesUnusedName(t *testing.T) {
	t.Parallel()

	fake := &s3SessionFake{}
	registry := NewS3SessionRegistry(fake, "recordings")

	if err := registry.Reserve(context.Background(), "Session-1"); err != nil {
		t.Fatalf("Reserve() returned %v", err)
	}
	if fake.putInput == nil {
		t.Fatal("Reserve() did not create a reservation object")
	}
	if got := aws.ToString(fake.putInput.Key); got != "Session-1/.kinugasa-session" {
		t.Fatalf("reservation key = %q", got)
	}
	if got := aws.ToString(fake.putInput.IfNoneMatch); got != "*" {
		t.Fatalf("IfNoneMatch = %q, want *", got)
	}
}

func TestS3SessionRegistryRejectsExistingPrefix(t *testing.T) {
	t.Parallel()

	fake := &s3SessionFake{listOutput: &s3.ListObjectsV2Output{Contents: []types.Object{{Key: aws.String("Session-1/take/camera/file.ts")}}}}
	registry := NewS3SessionRegistry(fake, "recordings")

	if err := registry.Reserve(context.Background(), "Session-1"); !errors.Is(err, ErrNameReserved) {
		t.Fatalf("Reserve() returned %v, want ErrNameReserved", err)
	}
	if fake.putInput != nil {
		t.Fatal("Reserve() attempted to overwrite an existing prefix")
	}
}

func TestS3SessionRegistryMapsConditionalConflict(t *testing.T) {
	t.Parallel()

	fake := &s3SessionFake{putError: &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "already exists"}}
	registry := NewS3SessionRegistry(fake, "recordings")

	if err := registry.Reserve(context.Background(), "Session-1"); !errors.Is(err, ErrNameReserved) {
		t.Fatalf("Reserve() returned %v, want ErrNameReserved", err)
	}
}

type s3SessionFake struct {
	listOutput *s3.ListObjectsV2Output
	listError  error
	putInput   *s3.PutObjectInput
	putError   error
}

func (fake *s3SessionFake) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if fake.listOutput == nil {
		return &s3.ListObjectsV2Output{}, fake.listError
	}

	return fake.listOutput, fake.listError
}

func (fake *s3SessionFake) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	fake.putInput = input
	return &s3.PutObjectOutput{}, fake.putError
}
