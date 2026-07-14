package operator

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHTTPUploaderStatusReader(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://uploader/status" {
			t.Fatalf("request URL = %q", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"uploader":{"phase":"Retrying","uploaded":{"one.ts":"a","two.ts":"b"}}}`)), Header: make(http.Header)}, nil
	})}
	status, err := (&HTTPUploaderStatusReader{Client: client}).Read(context.Background(), "http://uploader/status")
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "Retrying" || status.UploadedFiles != 2 {
		t.Fatalf("status = %#v", status)
	}
}
