package controller

func WithDryRunNodeLoad(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.DryRunNodeLoad = &val
	}
}

func WithDryRunClusterLoadDown(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.DryRunClusterLoadDown = &val
	}
}

func WithDryRunClusterLoadUp(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.DryRunClusterLoadUp = &val
	}
}
