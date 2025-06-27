FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY main.go .

RUN go build -o wol-agent main.go

FROM alpine:latest
RUN addgroup -g 1000 -S wol && adduser -u 1000 -S -G wol wol

# just for manual/cli testing purposes if required
RUN apk add --no-cache iputils

COPY --from=builder /app/wol-agent /usr/bin/wol-agent

RUN chown wol:wol /usr/bin/wol-agent
USER wol
ENTRYPOINT ["/usr/bin/wol-agent"]