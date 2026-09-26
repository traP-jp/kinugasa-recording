FROM rust:1.97.1-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends clang git libclang-dev meson ninja-build pkg-config \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY Cargo.toml Cargo.lock ./
COPY crates ./crates
RUN cargo build --locked --release -p kinugasa-app --bin kinugasa

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /recordings \
    && chown 65532:65532 /recordings

COPY --from=build /src/target/release/kinugasa /usr/local/bin/kinugasa

USER 65532:65532
EXPOSE 8080/tcp 4443/udp 9200-9299/udp
ENTRYPOINT ["/usr/local/bin/kinugasa"]
