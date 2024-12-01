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
	"time"

	"github.com/docent-net/cluster-bare-autoscaler/config"
	"github.com/docent-net/cluster-bare-autoscaler/utils/errors"

	"k8s.io/client-go/informers"
	kube_client "k8s.io/client-go/kubernetes"
)

// AutoscalerOptions is the whole set of options for configuring an autoscaler
type AutoscalerOptions struct {
	config.AutoscalingOptions
	KubeClient      kube_client.Interface
	InformerFactory informers.SharedInformerFactory
}

// Autoscaler is the main component of CA which scales up/down node groups according to its configuration
// The configuration can be injected at the creation of an autoscaler
type Autoscaler interface {
	// Start starts components running in background.
	Start() error
	// RunOnce represents an iteration in the control-loop of CA
	RunOnce(currentTime time.Time) errors.AutoscalerError
	// ExitCleanUp is a clean-up performed just before process termination.
	ExitCleanUp()
	// LastScaleUpTime is a time of the last scale up
	LastScaleUpTime() time.Time
	// LastScaleUpTime is a time of the last scale down
	LastScaleDownDeleteTime() time.Time
}

// NewAutoscaler creates an autoscaler of an appropriate type according to the parameters
func NewAutoscaler(opts AutoscalerOptions, informerFactory informers.SharedInformerFactory) (Autoscaler, errors.AutoscalerError) {
	err := initializeDefaultOptions(&opts, informerFactory)
	if err != nil {
		return nil, errors.ToAutoscalerError(errors.InternalError, err)
	}
	return NewStaticAutoscaler(), nil
}

// Initialize default options if not provided.
func initializeDefaultOptions(opts *AutoscalerOptions, informerFactory informers.SharedInformerFactory) error {

	return nil
}
