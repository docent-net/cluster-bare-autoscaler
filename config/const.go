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

package config

import "time"

const (
	// SchedulerConfigFileFlag is the name of the flag
	// for passing in custom scheduler config for in-tree scheduelr plugins
	SchedulerConfigFileFlag = "scheduler-config-file"

	// DefaultMaxClusterCores is the default maximum number of cores in the cluster.
	DefaultMaxClusterCores = 5000 * 64
	// DefaultMaxClusterMemory is the default maximum number of gigabytes of memory in cluster.
	DefaultMaxClusterMemory = 5000 * 64 * 20

	// DefaultScanInterval is the default scan interval for CA
	DefaultScanInterval = 10 * time.Second
)
