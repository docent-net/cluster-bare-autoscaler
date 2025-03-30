# syntax=docker/dockerfile:1
FROM cgr.dev/chainguard/static:latest

ARG VERSION
LABEL org.opencontainers.image.version=$VERSION

COPY bin/cluster-bare-autoscaler /cluster-bare-autoscaler

ENTRYPOINT ["/cluster-bare-autoscaler"]