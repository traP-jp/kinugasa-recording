FROM golang:1.26.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/video-worker ./cmd/video-worker

FROM bluenviron/mediamtx:1.20.1-ffmpeg
COPY --from=build /out/video-worker /usr/local/bin/video-worker
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/video-worker"]
