FROM golang:1.26.0-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/console-server ./cmd/console-server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/console-server /console-server
EXPOSE 8080 8081 8082 9090
ENTRYPOINT ["/console-server"]
