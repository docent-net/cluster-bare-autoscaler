package controller_test

import (
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/docent-net/cluster-bare-autoscaler/pkg/controller"
)

func TestReconcilerOptions(t *testing.T) {
	r := &controller.Reconciler{}

	val1 := 1.5
	val2 := 2.5
	val3 := 3.5

	controller.WithDryRunNodeLoad(val1)(r)
	require.Equal(t, &val1, r.DryRunNodeLoad)

	controller.WithDryRunClusterLoadDown(val2)(r)
	require.Equal(t, &val2, r.DryRunClusterLoadDown)

	controller.WithDryRunClusterLoadUp(val3)(r)
	require.Equal(t, &val3, r.DryRunClusterLoadUp)
}
