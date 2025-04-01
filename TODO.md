# TODO for Cluster Bare Autoscaler

## 🔧 In Progress / Next Up
- Improve config validation (e.g., thresholds > 0, cooldown sanity checks)
- Expose metrics via `/metrics` endpoint (Prometheus scrape)
- Refactor config loading in Helm chart (`.Values.config.*` directly passed to config.yaml)

## 📌 Planned / Backlog
- Implement real ScaleUp strategy (currently placeholder)
- Optional randomized polling interval to prevent thundering herd
- Strategy debug logging: report which strategy blocked scale-down
- Force node shutdown if repeated drain failures occur
- Build-time metadata injection (`-ldflags`) for version, commit, etc.
  -- Add GitHub Actions for lint/test/build/publish
- Optional alternative metrics collection via eBPF (future idea)
- Configurable image registry + repo in Helm (not hardcoded to Docker Hub)
- Optional ServiceMonitor support for metrics
- CLI help/usage enhancements with examples
- Optionally detect pod eviction stuck conditions

---

Feel free to contribute! Open issues or PRs welcome.

