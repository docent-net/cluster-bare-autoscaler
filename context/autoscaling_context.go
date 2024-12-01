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

package context

import (
	"github.com/docent-net/cluster-bare-autoscaler/clusterstate/utils"
	"github.com/docent-net/cluster-bare-autoscaler/config"
	kube_util "github.com/docent-net/cluster-bare-autoscaler/utils/kubernetes"
	kube_client "k8s.io/client-go/kubernetes"
	kube_record "k8s.io/client-go/tools/record"
)

// AutoscalingContext contains user-configurable constant and configuration-related objects passed to
// scale up/scale down functions.
type AutoscalingContext struct {
	// Options to customize how autoscaling works
	config.AutoscalingOptions
	// Kubernetes API clients.
	AutoscalingKubeClients
}

// AutoscalingKubeClients contains all Kubernetes API clients,
// including listers and event recorders.
type AutoscalingKubeClients struct {
	// Listers.
	kube_util.ListerRegistry
	// ClientSet interface.
	ClientSet kube_client.Interface
	// Recorder for recording events.
	Recorder kube_record.EventRecorder
	// LogRecorder can be used to collect log messages to expose via Events on some central object.
	LogRecorder *utils.LogEventRecorder
}
