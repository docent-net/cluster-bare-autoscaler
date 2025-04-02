# TODO for Cluster Bare Autoscaler

## 🔧 In Progress / Next Up
- Cluster-wide strategy in the ScaleUp chain: load evaluation (average, median, p90, p75) 
- ScaleUp trigger based on unschedulable pod events (e.g., from K8s scheduler)

## 📌 Planned / Backlog
- Alternative metrics agent using eBPF (instead of HTTP DaemonSet)
- Per-strategy Prometheus metrics
- Integration tests: simulate multi-node scenarios with mocks/fakes
- CLI enhancements: add usage examples, better validation
- Helm chart: registry override, versioning, optional ServiceMonitor
- Improve config validation (e.g., thresholds > 0, cooldown sanity checks)
- Refactor config loading in Helm chart (`.Values.config.*` passed to config.yaml)
- Strategy debug tracing: per-strategy logs for denied scale-down
- Add more spans to tracing
- Optional randomized polling interval to prevent thundering herd
- Detect pod eviction stuck conditions