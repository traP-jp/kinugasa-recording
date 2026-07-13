GO_COMPONENTS := operator video-fanout video-recorder video-uploader livekit-ingress

.PHONY: all build build-go build-web deploy fmt fmt-check generate help image-build lint test test-go test-web

all: fmt-check lint test build

help:
	@echo "Common targets: fmt fmt-check lint test build generate image-build deploy"

fmt:
	gofmt -w api cmd internal
	pnpm format

fmt-check:
	@files="$$(gofmt -l api cmd internal)"; test -z "$$files" || { echo "Go files need formatting:"; echo "$$files"; exit 1; }
	pnpm format:check

lint:
	go vet ./...
	golangci-lint run ./...
	pnpm lint

test: test-go test-web

test-go:
	go test ./...

test-web:
	pnpm test

build: build-go build-web

build-go:
	@mkdir -p bin
	@for component in $(GO_COMPONENTS); do \
		go build -o "bin/$$component" "./cmd/$$component"; \
	done

build-web:
	pnpm build

generate:
	./scripts/generate.sh

image-build:
	@echo "Container build definitions will be added in the container image phase."
	@exit 2

deploy:
	@echo "K3d deployment will be added in the Kubernetes environment phase."
	@exit 2
