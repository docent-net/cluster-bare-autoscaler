package nodeops

import v1 "k8s.io/api/core/v1"

type NodeAnnotationConfig struct {
	MAC string
}

// GetMACAddressFromNode returns the MAC address from a node's annotations.
func GetMACAddressFromNode(n v1.Node, keys NodeAnnotationConfig) string {
	key := keys.MAC
	if key == "" {
		key = AnnotationMACAuto
	}
	return n.Annotations[key]
}
