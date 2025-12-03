package config

import (
	"in-memorydb/pkg/structs"
)

const (
	defaultNodeID         = "node_1"
	defaultBindAddress    = "0.0.0.0"
	defaultExternalPort   = 50051
	defaultMembershipPort = 50052
	defaultGossipPort     = 50053

	defaultGossipProtocol   = "SWIM"
	defaultGossipIntervalMs = 500
	defaultGossipFanout     = 3
	defaultGossipRetries    = 3
)

var knownProtocols = structs.NewSet("SWIM")

var defaultNode = NodeConfig{
	ID:          defaultNodeID,
	BindAddress: defaultBindAddress,
	Port:        defaultExternalPort,
}

var defaultGossip = GossipConfig{
	Protocol:              defaultGossipProtocol,
	AntiEntropyIntervalMs: defaultGossipIntervalMs,
	Fanout:                defaultGossipFanout,
	Retries:               defaultGossipRetries,
}

var defaultPersistence = PersistenceConfig{
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

func (m *MembershipConfig) PopulateDefaults() {
	if m.Port == 0 {
		m.Port = defaultMembershipPort
	}
}

func (c *Config) PopulateDefaults() {
	c.Node.PopulateDefaults()
	c.Gossip.PopulateDefaults()
	c.Persistence.PopulateDefaults()
	c.Replication.PopulateDefaults()
	c.Security.PopulateDefaults()
	c.Membership.PopulateDefaults()

	c.Gossip.BindAddress = c.Node.BindAddress

}
