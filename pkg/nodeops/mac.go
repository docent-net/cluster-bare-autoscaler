package nodeops

import v1 "k8s.io/api/core/v1"

type NodeAnnotationConfig struct {
	MAC string
}

const DefaultMACAnnotation = "cba.dev/mac-address"

// GetMACAddressFromNode returns the MAC address from a node's annotations.
func GetMACAddressFromNode(n v1.Node, keys NodeAnnotationConfig) string {
	key := keys.MAC
	if key == "" {
		key = DefaultMACAnnotation
	}
	return n.Annotations[key]
}
