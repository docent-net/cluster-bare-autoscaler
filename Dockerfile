# syntax=docker/dockerfile:1
FROM cgr.dev/chainguard/static:latest

ARG VERSION
LABEL org.opencontainers.image.version=$VERSION

COPY bin/cluster-bare-autoscaler /cluster-bare-autoscaler

USER 65532  # UID of 'nonroot' user in Chainguard
ENTRYPOINT ["/cluster-bare-autoscaler"]