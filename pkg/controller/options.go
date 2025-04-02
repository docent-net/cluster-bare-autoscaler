package controller

func WithDryRunNodeLoad(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.dryRunNodeLoad = &val
	}
}

func WithDryRunClusterLoadDown(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.dryRunClusterLoadDown = &val
	}
}

func WithDryRunClusterLoadUp(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.dryRunClusterLoadUp = &val
	}
}
