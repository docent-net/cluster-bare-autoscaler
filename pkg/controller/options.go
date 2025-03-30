package controller

func WithDryRunNodeLoad(val float64) ReconcilerOption {
	return func(r *Reconciler) {
		r.dryRunNodeLoad = &val
	}
}
