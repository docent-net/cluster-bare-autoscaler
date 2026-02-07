FROM quay.io/helmpack/chart-testing:v3.12.0

RUN if command -v apk >/dev/null 2>&1; then \
      apk add --no-cache yamllint; \
    elif command -v apt-get >/dev/null 2>&1; then \
      apt-get update && apt-get install -y yamllint && rm -rf /var/lib/apt/lists/*; \
    else \
      exit 1; \
    fi
