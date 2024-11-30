package config

const (
	// SchedulerConfigFileFlag is the name of the flag
	// for passing in custom scheduler config for in-tree scheduelr plugins
	SchedulerConfigFileFlag = "scheduler-config-file"

	// DefaultMaxClusterCores is the default maximum number of cores in the cluster.
	DefaultMaxClusterCores = 5000 * 64
	// DefaultMaxClusterMemory is the default maximum number of gigabytes of memory in cluster.
	DefaultMaxClusterMemory = 5000 * 64 * 20
)
