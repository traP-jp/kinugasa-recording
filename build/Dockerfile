# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26
ARG NIX_VERSION=2.31.2

FROM golang:${GO_VERSION}-bookworm AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
ARG COMPONENT
RUN test -n "${COMPONENT}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /component "./cmd/${COMPONENT}"

FROM nixos/nix:${NIX_VERSION} AS ffmpeg-builder
WORKDIR /src
COPY flake.nix flake.lock ./
RUN ffmpeg_store="$(nix --extra-experimental-features 'nix-command flakes' build --no-link --print-out-paths .#ffmpeg)" \
    && mkdir /closure \
    && cp -a --parents $(nix-store -qR "$ffmpeg_store") /closure \
    && cp -aL "$ffmpeg_store" /ffmpeg

FROM debian:bookworm-slim AS go-runtime
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=go-builder /component /usr/local/bin/component
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/component"]

FROM debian:bookworm-slim AS media-runtime
RUN apt-get update \
    && apt-get install --no-install-recommends -y ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg-builder /closure/nix/store /nix/store
COPY --from=ffmpeg-builder /ffmpeg /opt/ffmpeg
COPY --from=go-builder /component /usr/local/bin/component
COPY --chmod=0755 build/check-ffmpeg.sh /usr/local/bin/check-ffmpeg
ENV PATH="/opt/ffmpeg/bin:${PATH}"
RUN check-ffmpeg
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/component"]
