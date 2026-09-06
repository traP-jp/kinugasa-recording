FROM rust:1.95.0-bookworm AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends clang git libclang-dev meson ninja-build pkg-config \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY video-gateway ./video-gateway
RUN cargo build --locked --release --manifest-path video-gateway/Cargo.toml

FROM debian:bookworm-slim
COPY --from=build /src/video-gateway/target/release/kinugasa-video-gateway /usr/local/bin/video-gateway
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/video-gateway"]
