package nodeops

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

const AnnotationPoweredOff = "cba.dev/was-powered-off"

// PoweredOffSince returns the timestamp when the node was marked powered-off,
// if present and parseable. If the annotation exists but isn't parseable,
// it returns Unix(0) to treat it as "very old".
func PoweredOffSince(n v1.Node) (time.Time, bool) {
	raw, ok := n.Annotations[AnnotationPoweredOff]
	if !ok || raw == "" {
		return time.Time{}, false
	}
	// RFC3339 preferred
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	// Back-compat with current writer format (e.g., 2006-01-02T15:04:05Z)
	if t, err := time.Parse("2006-01-02T15:04:05Z", raw); err == nil {
		return t.UTC(), true
	}
	// Unknown value → force to "oldest"
	return time.Unix(0, 0).UTC(), true
}
