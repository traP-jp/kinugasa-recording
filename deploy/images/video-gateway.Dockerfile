FROM golang:1.26.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/video-gateway ./cmd/video-gateway

FROM bluenviron/mediamtx:1.20.1-ffmpeg
RUN apk add --no-cache librist-progs \
    && ristreceiver --help 2>&1 | grep -q ristreceiver
COPY --from=build /out/video-gateway /usr/local/bin/video-gateway
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/video-gateway"]
