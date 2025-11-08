package config

import (
	"in-memorydb/pkg/structs"

	"github.com/google/uuid"
)

var knownProtocols = structs.NewSet("SWIM")

var defaultNode = NodeConfig{
	ID:          "node",
	BindAddress: "127.0.0.1",
	Port:        9090,
}

var defaultGossip = GossipConfig{
	Protocol:              "SWIM",
	AntiEntropyIntervalMs: 500,
	Fanout:                3,
	Retries:               3,
}

var defaultPersistence = PersistenceConfig{
	WalDir:             "wal",
	SnapDir:            "snap",
	SnapshotIntervalMs: 10,
}

var defaultReplication = ReplicationConfig{}

var defaultSecurity = SecurityConfig{
	Enabled: false,
}

func Default() *Config {
	return &Config{
		Node:        defaultNode,
		Gossip:      defaultGossip,
		Seeds:       []string{},
		Persistence: defaultPersistence,
		Replication: defaultReplication,
		Security:    defaultSecurity,
	}
}

func (c *NodeConfig) PopulateDefaults() {
	if c.BindAddress == "" {
		c.BindAddress = defaultNode.BindAddress
	}

	if c.Port == 0 {
		c.Port = defaultNode.Port
	}

	if c.ID == "" {
		c.ID = uuid.New().String()
	}
}

func (c *GossipConfig) PopulateDefaults() {
	if c.Protocol == "" {
		c.Protocol = defaultGossip.Protocol
	}

	if c.AntiEntropyIntervalMs == 0 {
		c.AntiEntropyIntervalMs = defaultGossip.AntiEntropyIntervalMs
	}

	if c.Fanout == 0 {
		c.Fanout = defaultGossip.Fanout
	}

	if c.Retries == 0 {
		c.Retries = defaultGossip.Retries
	}
}

func (c *PersistenceConfig) PopulateDefaults() {
	if c.WalDir == "" {
		c.WalDir = defaultPersistence.WalDir
	}

	if c.SnapDir == "" {
		c.SnapDir = defaultPersistence.SnapDir
	}

	if c.SnapshotIntervalMs == 0 {
		c.SnapshotIntervalMs = defaultPersistence.SnapshotIntervalMs
	}
}

func (c *ReplicationConfig) PopulateDefaults() {
	//
}

func (c *SecurityConfig) PopulateDefaults() {
	if !c.Enabled {
		return
	}
}

func (c *Config) PopulateDefaults() {
	c.Node.PopulateDefaults()
	c.Gossip.PopulateDefaults()
	c.Persistence.PopulateDefaults()
	c.Replication.PopulateDefaults()
	c.Security.PopulateDefaults()
}
