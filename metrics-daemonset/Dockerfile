# Build stage
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY main.go .
RUN go build -o loadavg-server main.go

# Minimal final image
FROM alpine:3.19
COPY --from=builder /app/loadavg-server /usr/bin/loadavg-server
ENTRYPOINT ["/usr/bin/loadavg-server"]