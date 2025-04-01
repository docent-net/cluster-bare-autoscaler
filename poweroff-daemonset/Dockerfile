# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go build -o shutdown-server main.go

# Minimal final image
FROM alpine:3.19
COPY --from=builder /app/shutdown-server /usr/bin/shutdown-server

# Ensure we can connect to the host's systemd socket
VOLUME ["/run/cba-shutdown.sock"]

ENTRYPOINT ["/usr/bin/shutdown-server"]