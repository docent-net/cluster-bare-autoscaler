/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This package was copied from the upstream Cluster Autoscaler repo and customized to work with Cluster Bare
Autoscaler
*/

package core

import (
	"github.com/docent-net/cluster-bare-autoscaler/context"
	"github.com/docent-net/cluster-bare-autoscaler/utils/errors"
	"time"
)

const (
	// How old the oldest unschedulable pod should be before starting scale up.
	unschedulablePodTimeBuffer = 2 * time.Second
	// How old the oldest unschedulable pod with GPU should be before starting scale up.
	// The idea is that nodes with GPU are very expensive and we're ready to sacrifice
	// a bit more latency to wait for more pods and make a more informed scale-up decision.
	unschedulablePodWithGpuTimeBuffer = 30 * time.Second

	// NodeUpcomingAnnotation is an annotation CA adds to nodes which are upcoming.
	NodeUpcomingAnnotation = "cluster-autoscaler.k8s.io/upcoming-node"

	// podScaleUpDelayAnnotationKey is an annotation how long pod can wait to be scaled up.
	podScaleUpDelayAnnotationKey = "cluster-autoscaler.kubernetes.io/pod-scale-up-delay"
)

// StaticAutoscaler is an autoscaler which has all the core functionality of a CBA but without the reconfiguration feature
type StaticAutoscaler struct {
	// AutoscalingContext consists of validated settings and options for this autoscaler
	*context.AutoscalingContext
	// ClusterState for maintaining the state of cluster nodes.
	lastScaleUpTime         time.Time
	lastScaleDownDeleteTime time.Time
	lastScaleDownFailTime   time.Time
	initialized             bool
}

// NewStaticAutoscaler creates an instance of Autoscaler filled with provided parameters
func NewStaticAutoscaler() *StaticAutoscaler {
	return &StaticAutoscaler{}
}

// Start starts components running in background.
func (a *StaticAutoscaler) Start() error {
	//a.clusterStateRegistry.Start()
	return nil
}

// RunOnce iterates over node groups and scales them up/down if necessary
func (a *StaticAutoscaler) RunOnce(currentTime time.Time) errors.AutoscalerError {
	return nil
}

// LastScaleUpTime returns last scale up time
func (a *StaticAutoscaler) LastScaleUpTime() time.Time {
	return a.lastScaleUpTime
}

// LastScaleDownDeleteTime returns the last successful scale down time
func (a *StaticAutoscaler) LastScaleDownDeleteTime() time.Time {
	return a.lastScaleDownDeleteTime
}

// ExitCleanUp performs all necessary clean-ups when the autoscaler's exiting.
func (a *StaticAutoscaler) ExitCleanUp() {
	//a.processors.CleanUp()
	//a.DebuggingSnapshotter.Cleanup()
	//
	//if !a.AutoscalingContext.WriteStatusConfigMap {
	//	return
	//}
	//utils.DeleteStatusConfigMap(a.AutoscalingContext.ClientSet, a.AutoscalingContext.ConfigNamespace, a.AutoscalingContext.StatusConfigMapName)
	//
	//a.CloudProvider.Cleanup()
	//
	//a.clusterStateRegistry.Stop()
}
