FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY main.go .

RUN go build -o wol-agent main.go

FROM alpine:3.19

# just for manual/cli testing purposes if required
RUN apk add --no-cache iputils

COPY --from=builder /app/wol-agent /usr/bin/wol-agent

ENTRYPOINT ["/usr/bin/wol-agent"]