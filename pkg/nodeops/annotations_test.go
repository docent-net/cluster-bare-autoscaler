package nodeops_test

import (
	"testing"
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/nodeops"
	v1 "k8s.io/api/core/v1"
)

func TestPoweredOffSince_NoAnnotation(t *testing.T) {
	n := v1.Node{}
	if _, ok := nodeops.PoweredOffSince(n); ok {
		t.Fatalf("expected ok=false when annotation is absent")
	}
}

func TestPoweredOffSince_RFC3339(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	n := v1.Node{ObjectMeta: mkObjMeta(map[string]string{nodeops.AnnotationPoweredOff: ts})}

	got, ok := nodeops.PoweredOffSince(n)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	want, _ := time.Parse(time.RFC3339, ts)
	if !got.Equal(want.UTC()) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestPoweredOffSince_InvalidBecomesOldest(t *testing.T) {
	n := v1.Node{ObjectMeta: mkObjMeta(map[string]string{nodeops.AnnotationPoweredOff: "true"})}

	got, ok := nodeops.PoweredOffSince(n)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !got.Equal(time.Unix(0, 0).UTC()) {
		t.Fatalf("got %v, want Unix(0)", got)
	}
}
