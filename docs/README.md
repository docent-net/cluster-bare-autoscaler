## Features

- Pluggable scale-down and scale-up strategies
  - Multi-strategy chaining with short-circuit logic
  - Dry-run mode for testing (`--dry-run`)
- Resource-aware scale-down
  - Considers CPU and memory requests
  - Optionally uses live usage metrics
- Load average-aware scale-down and scale-up using `/proc/loadavg`
  - Supports aggregation modes: `average`, `median`, `p75`, `p90`
  - Separate thresholds for scale-up and scale-down decisions
  - CLI dry-run overrides:
    - `--dry-run-cluster-load-down`
    - `--dry-run-cluster-load-up`
- MinNodeCount-based scale-up to maintain minimum node count
- Cooldown tracking
  - Global cooldown period
  - Per-node boot/shutdown cooldowns
- Node eligibility & label semantics
    - Managed: nodes with `cba.dev/is-managed` are in scope
    - Disabled: nodes with `cba.dev/disabled` are fully **excluded** from operations **and** from cluster-wide load math
    - ignoreLabels: presence/value rules exclude nodes from **operations** (scale/rotate), but they **still contribute** to aggregate load
- Safe cordon and drain using Kubernetes eviction API
- Wake-on-LAN support for powering on bare-metal machines
- Force power-on mode for maintenance
  - `forcePowerOnAllNodes: true` forces all previously powered-off nodes to be booted
  - Automatically clears `was-powered-off` annotation and uncordons nodes
- Rotation (wear leveling)
    - Opportunistic rotation on scale-up: the scaler prefers powering on the longest-powered-off node first (by `cba.dev/was-powered-off` timestamp)
    - Maintenance rotation: on loops with no scale action, CBA may retire one low-load node (respects `minNodes`, cooldowns, ignore/disabled labels, and load-avg thresholds if enabled)
- All containers run rootless


## Additional, optional services

- Metrics DaemonSet for per-node load average via `/proc/loadavg`
- Power-off DaemonSet for secure node shutdown via systemd socket activation
- Wake-on-LAN agent (wol-agent) for powering nodes via HTTP-triggered magic packets

---

## Quickstart

1. **Label your managed nodes**
   Apply a label to every node you want the autoscaler to manage:

   ```bash
   kubectl label node <node-name> cba.dev/is-managed=true
   ```

2. **(Optional) Manually annotate MAC addresses**
   This is needed if you use Wake-on-LAN (WOL) and cannot rely on auto-discovery:

   ```bash
   kubectl annotate node <node-name> cba.dev/mac-address-override=aa:bb:cc:dd:ee:ff
   ```

   If not manually annotated, MACs will be discovered from the node's local poweroff daemon Pod (via `/mac`) and stored in `cba.dev/mac-address`.

3. **Install the autoscaler with Helm**

   ```bash
   helm install cba cluster-bare-autoscaler/cluster-bare-autoscaler      -n <namespace>      -f values.yaml
   ```

4. **Observe behavior in dry-run mode (optional)**
   Run with `--dry-run` to observe decision logic without powering nodes.

---

## Node & annotation semantics

**Labels**

- `cba.dev/is-managed: "true"` — marks a node as managed by CBA (membership).
- `cba.dev/disabled: "true"` — **hard opt-out**: node is excluded from **all actions** and from **cluster-wide load math**.
- `ignoreLabels` (in `config.yaml`) — **soft ignore**: nodes matching these presence/value rules are **not acted upon** (no scale/rotate), but **do** count toward cluster-wide load.
- `loadAverageStrategy.excludeFromAggregateLabels` (in `config.yaml`) — **math-only exclude**: nodes matching these labels are **not counted** in cluster-wide load, but can still be acted upon unless also ignored/disabled.
    - **Recommended default** (set in your config): exclude control-plane/master from aggregate load:
      ```yaml
      loadAverageStrategy:
        excludeFromAggregateLabels:
          node-role.kubernetes.io/control-plane: ""
          node-role.kubernetes.io/master: ""
      ```

**Annotations**

| Key                               | Purpose                                                                 |
|-----------------------------------|-------------------------------------------------------------------------|
| `cba.dev/mac-address`             | Auto-discovered MAC for WoL                                            |
| `cba.dev/mac-address-override`    | Manually specified MAC (takes precedence)                               |
| `cba.dev/was-powered-off`         | RFC3339 timestamp when CBA shut the node down (presence means “off”)   |

> Note: `cba.dev/was-powered-off` is a timestamp (RFC3339). Legacy non-timestamp values are treated as “very old” and get normalized on the next shutdown.

---

### Rotation (wear leveling) behavior

- **Scale-up preference:** when powering on, CBA orders candidates by **longest-powered-off first** (from `cba.dev/was-powered-off`).

- **Maintenance rotation (two-phase; runs only if no scale up/down happened in the loop):**
    1) Find the **oldest** managed node marked powered-off whose off-age ≥ `rotation.maxPoweredOffDuration`
        - respects `rotation.exemptLabel` and `ignoreLabels`
    2) **Pre-checks before booting:**
        - capacity guard: `eligible + 1 > minNodes`
        - if **LoadAverage** is enabled, ensure there exists a tentative retire candidate that would pass the same gates as scale-down:
            - candidate node normalized load `< nodeThreshold`
            - cluster aggregate load (computed with `loadAverageStrategy.excludeFromAggregateLabels` and **excluding the candidate**) `< scaleDownThreshold`
    3) **Power on** the overdue node and **return** (no same-loop shutdown). Readiness and stabilization are enforced by:
        - **global cooldown** (op-to-op pacing), and
        - **bootCooldown** (prevents the freshly booted node from being a shutdown candidate)
    4) On later loops—once the node is **Ready** and cooldowns have elapsed—normal logic may retire **exactly one** eligible active node.

- If power-on fails, rotation **aborts**; no shutdown is attempted.

---


## Metrics

The autoscaler exposes Prometheus metrics on port `:9090` at the `/metrics` endpoint.
Metrics include evaluation counts, shutdown attempts/successes, eviction failures, and per-node powered-off status.

---

## Installation

### Using Helm

```bash
helm install cba cluster-bare-autoscaler/cluster-bare-autoscaler   -n <namespace>   -f values.yaml
```

---

## Configuration

All configuration is passed via a ConfigMap in YAML form.
The root config keys are defined in the `values.yaml`.

See the example `config.yaml` for details.

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

### Integration(-ish) tests

We keep unit tests next to packages, and black-box integration tests in a separate tree:

```bash
test/
integration/
controller_integration_test.go # scenarios across full reconcile loops
scenario/
scenario.go # shared fakes & builders
```

Integration tests are guarded by a build tag and don’t run by default:

```bash
```bash
# unit tests (default)
go test ./...

# integration tests (black-box end-to-end-ish)
go test -tags=integration ./test/integration -v
```

They use client-go fakes + small mocks for power/shutdown to simulate multi-node clusters.

---

## Roadmap & TODO

See `TODO.md` for the current feature backlog and development roadmap.

---

## Troubleshooting

### CBA logs: `the server is currently unable to handle the request (get nodes.metrics.k8s.io)`

**What it means:**
CBA tried to read live CPU and memory usage from the Kubernetes metrics API, but your cluster's `metrics.k8s.io` endpoint is registered without a working backend.

**How to confirm:**

```bash
kubectl get apiservice v1beta1.metrics.k8s.io
# STATUS: False (MissingEndpoints)
kubectl -n kube-system get svc metrics-server
kubectl -n kube-system get deploy metrics-server

---

## License

MIT
