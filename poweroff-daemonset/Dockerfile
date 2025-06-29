# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go build -o shutdown-server main.go

# Minimal final image
FROM alpine:latest

# Match GID to the host-side group (e.g., `cba:x:1050:`)
RUN addgroup -g 1050 -S cba && adduser -u 1050 -S -G cba cba

RUN apk add --no-cache iputils

COPY --from=builder /app/shutdown-server /usr/bin/shutdown-server
RUN chown cba:cba /usr/bin/shutdown-server

# Ensure we can connect to the host's systemd socket
VOLUME ["/run/cba-shutdown.sock"]

USER cba
ENTRYPOINT ["/usr/bin/shutdown-server"]