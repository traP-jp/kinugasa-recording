GO_COMPONENTS := operator video-fanout video-recorder video-uploader livekit-ingress
IMAGE_PREFIX ?= kinugasa-recording
IMAGE_TAG ?= latest
MEDIA_COMPONENTS := video-fanout video-recorder livekit-ingress

.PHONY: all build build-go build-web deploy fmt fmt-check generate help image-build k3d-create k3d-destroy k3d-import lint test test-go test-integration test-web

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

test-integration:
	./test/integration/session-workloads.sh
	./test/integration/media-fanout.sh
	./test/integration/recording-upload.sh

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
	@for component in $(GO_COMPONENTS); do \
		target=go-runtime; \
		case " $(MEDIA_COMPONENTS) " in *" $$component "*) target=media-runtime ;; esac; \
		docker build --build-arg COMPONENT="$$component" --target="$$target" \
			-t "$(IMAGE_PREFIX)/$$component:$(IMAGE_TAG)" -f build/Dockerfile . || exit; \
	done
	docker build -t "$(IMAGE_PREFIX)/web:$(IMAGE_TAG)" -f build/Dockerfile.web .

deploy:
	./scripts/k3d-deploy.sh

k3d-create:
	./scripts/k3d-create.sh

k3d-import:
	./scripts/k3d-import.sh

k3d-destroy:
	./scripts/k3d-destroy.sh
