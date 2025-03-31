FROM golang:1.22-alpine as builder
WORKDIR /app
COPY main.go .
RUN go build -o shutdown-server main.go

FROM alpine:3.19
COPY --from=builder /app/shutdown-server /usr/bin/shutdown-server
ENTRYPOINT ["/usr/bin/shutdown-server"]