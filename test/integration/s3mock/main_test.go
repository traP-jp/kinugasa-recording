package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerStoresAndListsObjects(t *testing.T) {
	t.Parallel()
	mock := &server{objects: map[string]object{}}

	put := httptest.NewRequest(http.MethodPut, "/bucket/session/take/camera/segment.ts", strings.NewReader("mpegts"))
	put.Header.Set("Content-Type", "video/mp2t")
	put.Header.Set("X-Amz-Meta-Sha256", "digest")
	putResponse := httptest.NewRecorder()
	mock.ServeHTTP(putResponse, put)
	if putResponse.Code != http.StatusOK {
		t.Fatalf("PUT status = %d", putResponse.Code)
	}

	getResponse := httptest.NewRecorder()
	mock.ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/bucket/session/take/camera/segment.ts", nil))
	contents, _ := io.ReadAll(getResponse.Result().Body)
	_ = getResponse.Result().Body.Close()
	if string(contents) != "mpegts" || getResponse.Header().Get("Content-Type") != "video/mp2t" || getResponse.Header().Get("X-Amz-Meta-Sha256") != "digest" {
		t.Fatalf("GET response = headers %#v body %q", getResponse.Header(), contents)
	}

	listResponse := httptest.NewRecorder()
	mock.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/_objects", nil))
	if body := strings.TrimSpace(listResponse.Body.String()); body != `["bucket/session/take/camera/segment.ts"]` {
		t.Fatalf("objects = %s", body)
	}
}

func TestServerHonorsPutIfNoneMatch(t *testing.T) {
	t.Parallel()
	mock := &server{objects: map[string]object{"bucket/key": {body: []byte("existing")}}}
	request := httptest.NewRequest(http.MethodPut, "/bucket/key", strings.NewReader("replacement"))
	request.Header.Set("If-None-Match", "*")
	response := httptest.NewRecorder()
	mock.ServeHTTP(response, request)
	if response.Code != http.StatusPreconditionFailed || string(mock.objects["bucket/key"].body) != "existing" {
		t.Fatalf("status = %d, object = %q", response.Code, mock.objects["bucket/key"].body)
	}
}
