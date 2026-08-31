FROM golang:1.26.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/video-uploader ./cmd/video-uploader

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/video-uploader /video-uploader
ENTRYPOINT ["/video-uploader"]
