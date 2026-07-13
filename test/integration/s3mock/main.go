package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type object struct {
	body        []byte
	contentType string
	sha256      string
}

type server struct {
	mutex   sync.RWMutex
	objects map[string]object
}

func main() {
	address := os.Getenv("S3MOCK_ADDRESS")
	if address == "" {
		address = ":19000"
	}
	mock := &server{objects: map[string]object{}}
	httpServer := &http.Server{Addr: address, Handler: mock, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("integration S3 mock listening on %s", address)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (mock *server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/_health" {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if request.URL.Path == "/_objects" {
		mock.list(response)
		return
	}
	key := strings.TrimPrefix(request.URL.EscapedPath(), "/")
	if key == "" || !strings.Contains(key, "/") {
		writeS3Error(response, http.StatusBadRequest, "InvalidURI")
		return
	}
	switch request.Method {
	case http.MethodHead:
		mock.head(response, key)
	case http.MethodGet:
		mock.get(response, key)
	case http.MethodPut:
		mock.put(response, request, key)
	default:
		response.Header().Set("Allow", "GET, HEAD, PUT")
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (mock *server) list(response http.ResponseWriter) {
	mock.mutex.RLock()
	keys := make([]string, 0, len(mock.objects))
	for key := range mock.objects {
		keys = append(keys, key)
	}
	mock.mutex.RUnlock()
	sort.Strings(keys)
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(keys)
}

func (mock *server) head(response http.ResponseWriter, key string) {
	mock.mutex.RLock()
	value, found := mock.objects[key]
	mock.mutex.RUnlock()
	if !found {
		writeS3Error(response, http.StatusNotFound, "NoSuchKey")
		return
	}
	writeObjectHeaders(response, value)
	response.WriteHeader(http.StatusOK)
}

func (mock *server) get(response http.ResponseWriter, key string) {
	mock.mutex.RLock()
	value, found := mock.objects[key]
	mock.mutex.RUnlock()
	if !found {
		writeS3Error(response, http.StatusNotFound, "NoSuchKey")
		return
	}
	writeObjectHeaders(response, value)
	_, _ = response.Write(value.body)
}

func (mock *server) put(response http.ResponseWriter, request *http.Request, key string) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<30))
	if err != nil {
		writeS3Error(response, http.StatusBadRequest, "InvalidRequest")
		return
	}
	mock.mutex.Lock()
	defer mock.mutex.Unlock()
	if _, found := mock.objects[key]; found && request.Header.Get("If-None-Match") == "*" {
		writeS3Error(response, http.StatusPreconditionFailed, "PreconditionFailed")
		return
	}
	mock.objects[key] = object{body: body, contentType: request.Header.Get("Content-Type"), sha256: request.Header.Get("X-Amz-Meta-Sha256")}
	response.Header().Set("ETag", fmt.Sprintf("\"integration-%d\"", len(body)))
	response.WriteHeader(http.StatusOK)
}

func writeObjectHeaders(response http.ResponseWriter, value object) {
	response.Header().Set("Content-Type", value.contentType)
	response.Header().Set("X-Amz-Meta-Sha256", value.sha256)
	response.Header().Set("Content-Length", fmt.Sprintf("%d", len(value.body)))
}

func writeS3Error(response http.ResponseWriter, status int, code string) {
	response.Header().Set("Content-Type", "application/xml")
	response.WriteHeader(status)
	_, _ = fmt.Fprintf(response, "<Error><Code>%s</Code><Message>%s</Message></Error>", code, code)
}
