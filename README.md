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

- ✅ Pluggable scale-down **and scale-up** strategies
- ✅ Resource-aware scale-down (CPU/mem request + usage)
- ✅ Load average-aware scale-down using `/proc/loadavg`
- ✅ Cluster-wide load evaluation (average, median, p90, p75) in LoadAverageStrategy
- ✅ **MinNodeCount-based scale-up** to maintain minimum node count
- ✅ Multi-strategy chaining (short-circuit logic)
- ✅ Dry-run mode for testing, with CLI overrides
- ✅ Cooldown tracking (global + per-node boot/shutdown)
- ✅ Nodes marked with `cba.dev/was-powered-off: true` or `.Disabled` are ignored
- ✅ Safe cordon and drain before shutdown
- ✅ Wake-on-LAN support for bare-metal power-on

## Additional, optionable services

- ✅ Metrics DaemonSet for per-node load average via `/proc/loadavg`
- ✅ Power-off DaemonSet for secure node shutdown via systemd socket activation
- ✅ Wake-on-LAN agent (wol-agent) for powering nodes via HTTP-triggered magic packets
- 
---

## Metrics

The autoscaler exposes Prometheus metrics on port `:9090` at the `/metrics` endpoint. Metrics include evaluation counts, shutdown attempts/successes, eviction failures, and per-node powered-off status.

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
