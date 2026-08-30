package s3store

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestPutObjectIncludesContentMetadata(t *testing.T) {
	client := &putObjectStub{}
	store := &Store{client: client, bucket: "recordings"}
	digest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := store.Put(
		context.Background(),
		"recording/session/take/camera/hash-video.mp4",
		bytes.NewReader([]byte("video")),
		5,
		digest,
		"video/mp4",
	); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	input := client.input
	if input == nil || *input.Bucket != "recordings" || *input.ContentLength != 5 ||
		*input.ContentType != "video/mp4" || input.Metadata["sha256"] != digest {
		t.Fatalf("PutObject input = %+v", input)
	}
	content, err := io.ReadAll(input.Body)
	if err != nil || string(content) != "video" {
		t.Fatalf("PutObject body = %q, %v", content, err)
	}
}

type putObjectStub struct{ input *s3.PutObjectInput }

func (s *putObjectStub) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	s.input = input
	return &s3.PutObjectOutput{}, nil
}
