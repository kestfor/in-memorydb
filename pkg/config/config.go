package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Node        NodeConfig        `yaml:"node"`
	Gossip      GossipConfig      `yaml:"gossip"`
	Seeds       []string          `yaml:"seeds"`
	Persistence PersistenceConfig `yaml:"persistence"`
	Replication ReplicationConfig `yaml:"replication"`
	Security    SecurityConfig    `yaml:"security"`
}

type NodeConfig struct {
	ID          string `yaml:"id"`
	BindAddress string `yaml:"bind_address"`
	Port        int    `yaml:"port"`
}

type SecurityConfig struct {
	Enabled bool   `yaml:"enabled"`
	CaCert  string `yaml:"ca_cert"`
	CaKey   string `yaml:"ca_key"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

type GossipConfig struct {
	Protocol            string `yaml:"protocol"`
	AntiEntropyInterval int    `yaml:"interval"`
	Fanout              int    `yaml:"fanout"`
	Retries             int    `yaml:"retries"`
}

type PersistenceConfig struct {
	WalDir           string `yaml:"wal_dir"`
	SnapDir          string `yaml:"snap_dir"`
	SnapshotInterval int    `yaml:"snapshot_interval"`
}

type ReplicationConfig struct {
}

func Read(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
