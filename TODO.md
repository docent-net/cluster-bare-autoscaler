# TODO for Cluster Bare Autoscaler

## ✅ Completed (recent)
- Core scale-down logic (cordon, drain, shutdown)
- Global and per-node cooldown logic
- ResourceAware strategy with CPU/memory requests + usage
- LoadAverage strategy based on DaemonSet metrics `/proc/loadavg`
- Strategy chaining with short-circuiting
- CLI dry-run override for node load (`--dry-run-node-load`)
- Basic Prometheus-style metrics collection
- Helm chart with flexible configuration
- Metrics DaemonSet with custom labels and tolerations
- Makefile and multi-arch `ko` image builds

---

## 🔧 In Progress / Next Up
- Add cluster-wide load evaluation (average, median, or p90) to LoadAverageStrategy
- Add unit tests for LoadAverageStrategy (global logic, edge cases)
- Improve config validation (e.g., thresholds > 0, cooldown sanity checks)
- Expose metrics via `/metrics` endpoint (Prometheus scrape)
- Refactor config loading in Helm chart (`.Values.config.*` directly passed to config.yaml)

---

## 📌 Planned / Backlog
- Implement real ScaleUp strategy (currently placeholder)
- Optional randomized polling interval to prevent thundering herd
- Strategy debug logging: report which strategy blocked scale-down
- Force node shutdown if repeated drain failures occur
- Build-time metadata injection (`-ldflags`) for version, commit, etc.
- Add GitHub Actions for lint/test/build/publish
- Optional alternative metrics collection via eBPF (future idea)
- Configurable image registry + repo in Helm (not hardcoded to Docker Hub)
- Optional ServiceMonitor support for metrics
- CLI help/usage enhancements with examples
- Optionally detect pod eviction stuck conditions

---

## 🧪 Testing Ideas
- End-to-end dry-run tests for scale-down and scale-up paths
- Unit tests for individual strategy decisions
- Simulated CPU/mem load tests with mocked metrics API responses
- Validate strategy chain behavior with combinations of passing/failing conditions

---

Feel free to contribute! Open issues or PRs welcome.

