package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type NodeConfig struct {
	Name string `yaml:"name"`
	IP   string `yaml:"ip"`
}

type Config struct {
	MinNodes     int               `yaml:"minNodes"`
	Cooldown     time.Duration     `yaml:"cooldown"`
	PollInterval time.Duration     `yaml:"pollInterval"`
	IgnoreLabels map[string]string `yaml:"ignoreLabels"`
	Nodes        []NodeConfig      `yaml:"nodes"`
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
