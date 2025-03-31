![Go CI](https://github.com/docent-net/cluster-bare-autoscaler/actions/workflows/go-test.yaml/badge.svg)
[![codecov](https://codecov.io/gh/docent-net/cluster-bare-autoscaler/branch/main/graph/badge.svg)](https://codecov.io/gh/docent-net/cluster-bare-autoscaler)

# Cluster Bare Autoscaler

## Introduction

**Cluster Bare Autoscaler (CBA)** automatically adjusts the size of a bare-metal Kubernetes cluster by powering nodes off or on based on real-time resource usage, while safely cordoning and draining nodes before shutdown.

This project is similar to the official [Kubernetes Cluster Autoscaler](https://github.com/kubernetes/autoscaler/tree/master/cluster-autoscaler), but with key differences:
- CBA does **not terminate or bootstrap instances**.
- Instead, it powers down and wakes up bare-metal nodes using mechanisms like **Wake-on-LAN** (the only implemented one now), or other pluggable power controllers.
- Nodes are **cordoned and drained safely** before shutdown.

CBA uses a **chainable strategy model** for deciding when to scale down a node. Strategies can be enabled individually or used together:
- **Resource-aware strategy** — checks CPU and memory requests and usage.
- **Load average strategy** — evaluates `/proc/loadavg` via a per-node metrics DaemonSet.

It is especially suited for **self-managed data centers**, **homelabs**, or **cloud-like bare-metal environments**.

---

## Features

- ✅ Pluggable scale-down strategies
- ✅ Resource-aware scale-down (CPU/mem request + usage)
- ✅ Load average-aware scale-down using `/proc/loadavg`
- ✅ Cluster-wide load evaluation (average, median, or p90) in LoadAverageStrategy
- ✅ Multi-strategy support with short-circuit logic
- ✅ Dry-run mode for testing, including cluster-level overrides
- ✅ Cooldown tracking (global + per-node)
- ✅ Metrics daemonset for per-node loadavg
- ✅ Nodes marked with `cba.dev/was-powered-off: true` are excluded from scaling 
    logic until manually cleared or rebooted via CBA."
- ✅ Manual reboot requires annotation cleanup.
- ✅ Optional Helm chart for deployment
- ✅ Compatible with Wake-on-LAN (for now, can be extended with IPMI etc)
- ✅ Safe cordon and drain before shutdown
- ✅ Comprehensive unit tests for LoadAverageStrategy and edge cases

---

## Installation

### Using Helm

```bash
helm install cba cluster-bare-autoscaler/cluster-bare-autoscaler \
  -n <namespace> \
  -f values.yaml
```

---

## Configuration

All configuration is passed via a ConfigMap in YAML form.
The root config keys are defined in the [values.yaml](helm/values.yaml)

---

## Development

### Build the binary
```bash
make build_binary
```

### Run tests
```bash
make test
```

### Build container image (multi-arch with `ko`)
```bash
make build_image
make publish_image
```

### Run locally
```bash
go run main.go --config=./config.yaml --dry-run
```

---

## Roadmap & TODO

See [TODO.md](TODO.md) for the current feature backlog and development roadmap.

---

## License

MIT
