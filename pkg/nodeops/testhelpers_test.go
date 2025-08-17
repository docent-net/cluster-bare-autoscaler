package nodeops_test

import (
	v1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func nodeWith(name string, labels, ann map[string]string) v1.Node {
	return v1.Node{
		ObjectMeta: meta.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: ann,
		},
	}
}

func mkObjMeta(ann map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Annotations: ann}
}
