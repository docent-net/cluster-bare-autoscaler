# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go build -o metrics-server main.go

FROM alpine:latest

# Add non-root user with specific UID/GID
RUN addgroup -g 1050 -S cba && adduser -u 1050 -S -G cba cba

COPY --from=builder /app/metrics-server /usr/bin/metrics-server

RUN chown cba:cba /usr/bin/metrics-server

USER cba
ENTRYPOINT ["/usr/bin/metrics-server"]