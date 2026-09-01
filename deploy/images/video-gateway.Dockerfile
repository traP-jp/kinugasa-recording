FROM alpine:3.23
RUN apk add --no-cache librist-progs \
    && ristreceiver --help 2>&1 | grep -q ristreceiver
USER 65532:65532
ENTRYPOINT ["/usr/bin/ristreceiver"]
