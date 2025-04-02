package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type NodeConfig struct {
	Name       string `yaml:"name"`
	IP         string `yaml:"ip"`
	Disabled   bool   `yaml:"disabled,omitempty"`
	WOLMacAddr string `yaml:"wolMacAddr,omitempty"`
}

type Config struct {
	LogLevel string `yaml:"logLevel"`

	MinNodes     int               `yaml:"minNodes"`
	Cooldown     time.Duration     `yaml:"cooldown"`
	BootCooldown time.Duration     `yaml:"bootCooldown"`
	PollInterval time.Duration     `yaml:"pollInterval"`
	IgnoreLabels map[string]string `yaml:"ignoreLabels"`
	Nodes        []NodeConfig      `yaml:"nodes"`

	ResourceBufferCPUPerc    int `yaml:"resourceBufferCPUPerc"`
	ResourceBufferMemoryPerc int `yaml:"resourceBufferMemoryPerc"`

	DryRun                   bool `yaml:"dryRun"` // NEW: dry-run mode
	BootstrapCooldownSeconds int  `yaml:"bootstrapCooldownSeconds"`

	LoadAverageStrategy LoadAverageStrategyConfig `yaml:"loadAverageStrategy"`
	ShutdownManager     ShutdownManagerConfig     `yaml:"shutdownManager"`
	ShutdownMode        string                    `yaml:"shutdownMode"` // supported: "http", "disabled"

	PowerOnMode       string         `yaml:"powerOnMode"` // "disabled", "wol"
	WOLBroadcastAddr  string         `yaml:"wolBroadcastAddr"`
	WOLBootTimeoutSec int            `yaml:"wolBootTimeoutSeconds"`
	WolAgent          WolAgentConfig `yaml:"wolAgent"`
}

type LoadAverageStrategyConfig struct {
	Enabled              bool    `yaml:"enabled"`
	NodeThreshold        float64 `yaml:"nodeThreshold"`
	ClusterWideThreshold float64 `yaml:"clusterWideThreshold"`
	PodLabel             string  `yaml:"podLabel"`
	Namespace            string  `yaml:"namespace"`
	Port                 int     `yaml:"port"`
	TimeoutSeconds       int     `yaml:"timeoutSeconds"`
	ClusterEval          string  `yaml:"clusterEval,omitempty"` // "average", "median", "p90", "p75"
}

type ShutdownManagerConfig struct {
	Port      int    `yaml:"port"`
	Namespace string `yaml:"namespace"`
	PodLabel  string `yaml:"podLabel"`
}
type WolAgentConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Port      int    `yaml:"port"`
	Namespace string `yaml:"namespace"`
	PodLabel  string `yaml:"podLabel"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
