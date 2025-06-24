package config

import (
	"fmt"
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

type NodeLabelConfig struct {
	Managed  string `yaml:"managed"`
	Disabled string `yaml:"disabled"`
}

type NodeAnnotationConfig struct {
	MAC string `yaml:"mac"`
}

type Config struct {
	LogLevel string `yaml:"logLevel"`

	MinNodes        int                  `yaml:"minNodes"`
	Cooldown        time.Duration        `yaml:"cooldown"`
	BootCooldown    time.Duration        `yaml:"bootCooldown"`
	PollInterval    time.Duration        `yaml:"pollInterval"`
	IgnoreLabels    map[string]string    `yaml:"ignoreLabels"`
	NodeLabels      NodeLabelConfig      `yaml:"nodeLabels"`
	NodeAnnotations NodeAnnotationConfig `yaml:"nodeAnnotations"`

	ResourceBufferCPUPerc    int `yaml:"resourceBufferCPUPerc"`
	ResourceBufferMemoryPerc int `yaml:"resourceBufferMemoryPerc"`

	DryRun                   bool `yaml:"dryRun"` // NEW: dry-run mode
	BootstrapCooldownSeconds int  `yaml:"bootstrapCooldownSeconds"`

	LoadAverageStrategy LoadAverageStrategyConfig `yaml:"loadAverageStrategy"`
	ShutdownManager     ShutdownManagerConfig     `yaml:"shutdownManager"`
	ShutdownMode        string                    `yaml:"shutdownMode"` // supported: "http", "disabled"

	PowerOnMode          string         `yaml:"powerOnMode"` // "disabled", "wol"
	WOLBroadcastAddr     string         `yaml:"wolBroadcastAddr"`
	WOLBootTimeoutSec    int            `yaml:"wolBootTimeoutSeconds"`
	WolAgent             WolAgentConfig `yaml:"wolAgent"`
	MACDiscoveryInterval time.Duration  `yaml:"macDiscoveryIntervalMin"`

	ForcePowerOnAllNodes bool `yaml:"forcePowerOnAllNodes"`
}

type LoadAverageStrategyConfig struct {
	Enabled            bool    `yaml:"enabled"`
	NodeThreshold      float64 `yaml:"nodeThreshold"`
	ScaleDownThreshold float64 `yaml:"scaleDownThreshold"`
	ScaleUpThreshold   float64 `yaml:"scaleUpThreshold"`
	PodLabel           string  `yaml:"podLabel"`
	Namespace          string  `yaml:"namespace"`
	Port               int     `yaml:"port"`
	TimeoutSeconds     int     `yaml:"timeoutSeconds"`
	ClusterEval        string  `yaml:"clusterEval,omitempty"` // "average", "median", "p90", "p75"
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
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if err := cfg.ApplyDefaultsAndValidate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (cfg *Config) ApplyDefaultsAndValidate() error {
	if cfg.MACDiscoveryInterval == 0 {
		cfg.MACDiscoveryInterval = 30 * time.Minute
	}

	if cfg.MACDiscoveryInterval < 10*time.Second {
		return fmt.Errorf("macDiscoveryInterval too short: %s", cfg.MACDiscoveryInterval)
	}

	// Add more defaults/validations here later

	return nil
}
