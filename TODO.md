# TODO for Cluster Bare Autoscaler

## 🔧 In Progress / Next Up
- Increase unit tests coverage

## 📌 Planned / Backlog
- APM tracing & profiling (otel)
- Otel metrics and dashboards
- Documentation with examples, quick start etc
- Helm chart: registry override, versioning, optional ServiceMonitor
- ScaleUp trigger based on unschedulable pod events (e.g., from K8s scheduler)
- Drain-aware scale-down
- minNodesPerGroup enforcement for scale-down
- Alternative metrics agent using eBPF (instead of HTTP DaemonSet)
- Per-strategy Prometheus and otel metrics
- Integration tests: simulate multi-node scenarios with mocks/fakes
- CLI enhancements: add usage examples, better validation
- Improve config validation (e.g., thresholds > 0, cooldown sanity checks)
- Detect pod eviction stuck conditions
- Use retry.OnError to handle update conflicts across the codebase ([#20](https://github.com/docent-net/cluster-bare-autoscaler/issues/20))