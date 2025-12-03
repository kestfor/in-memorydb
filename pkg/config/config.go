package config

import (
	wal "in-memorydb/pkg/storage/wal/poc"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Node        NodeConfig        `yaml:"node"`
	Gossip      GossipConfig      `yaml:"gossip"`
	Membership  MembershipConfig  `yaml:"membership"`
	Seeds       []string          `yaml:"seeds"`
	Persistence PersistenceConfig `yaml:"persistence"`
	Replication ReplicationConfig `yaml:"replication"`
	Security    SecurityConfig    `yaml:"security"`
	Transport   TransportConfig   `yaml:"transport"`
	TraceConfig TraceConfig       `yaml:"trace"`
}

type MembershipConfig struct {
	Port uint16 `yaml:"port"`
}

type TraceConfig struct {
	Enable bool `yaml:"enable"`
}

type TransportConfig struct{}

type NodeConfig struct {
	ID          string `yaml:"id"`
	BindAddress string `yaml:"bind_address"`
	Port        uint16 `yaml:"port"`
}

type SecurityConfig struct {
	Enabled bool   `yaml:"enabled"`
	CaCert  string `yaml:"ca_cert"`
	CaKey   string `yaml:"ca_key"`
	Cert    string `yaml:"cert"`
	Key     string `yaml:"key"`
}

type GossipConfig struct {
	BindAddress           string `yaml:"-"`
	Port                  uint16 `yaml:"port"`
	Protocol              string `yaml:"protocol"`
	AntiEntropyIntervalMs int    `yaml:"interval"`
	Fanout                int    `yaml:"fanout"`
	Retries               int    `yaml:"retries"`
}

type PersistenceConfig struct {
	WalConfig          wal.Config `yaml:"wal"`
	SnapDir            string     `yaml:"snap_dir"`
	SnapshotIntervalMs int        `yaml:"snapshot_interval"`
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

	cfg.PopulateDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}
