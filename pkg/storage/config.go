package storage

import (
	"in-memorydb/pkg/gossip/gossip"
	"in-memorydb/pkg/storage/wal/v1"
	transport "in-memorydb/pkg/transport/grpc"
)

type Config struct {
	Node        NodeConfig                `yaml:"node"`
	Gossip      gossip.Config             `yaml:"gossip"`
	Membership  MembershipConfig          `yaml:"membership"`
	Seeds       []string                  `yaml:"seeds"`
	Persistence PersistenceConfig         `yaml:"persistence"`
	Replication ReplicationConfig         `yaml:"replication"`
	Security    SecurityConfig            `yaml:"security"`
	Transport   transport.TransportConfig `yaml:"transport"`
	TraceConfig TraceConfig               `yaml:"trace"`
}

type TraceConfig struct {
	Endpoint string `yaml:"endpoint" env:"TRACE_ENDPOINT" required:"true" default:"localhost:4318"`
	Enable   bool   `yaml:"enable" env:"TRACE_ENABLE" default:"false"`
}

type NodeConfig struct {
	ID          string `yaml:"id" env:"NODE_ID" required:"true"`
	BindAddress string `yaml:"bind_address" env:"NODE_BIND_ADDRESS" required:"true" default:"0.0.0.0"`
	Port        uint16 `yaml:"port" env:"NODE_PORT" required:"true" default:"50051"`
}

type MembershipConfig struct {
	Port uint16 `yaml:"port" env:"MEMBERSHIP_PORT" required:"true" default:"50053"`
}

type SecurityConfig struct {
	Enabled bool   `yaml:"enabled" env:"SECURITY_ENABLED" default:"false"`
	CaCert  string `yaml:"ca_cert" env:"SECURITY_CA_CERT_FILE" default:"/etc/secrets/ca.crt"`
	CaKey   string `yaml:"ca_key" env:"SECURITY_CA_KEY" default:"/etc/secrets/ca.key"`
	Cert    string `yaml:"cert" env:"SECURITY_CERT" default:"/etc/secrets/tls.crt"`
	Key     string `yaml:"key" env:"SECURITY_KEY" default:"/etc/secrets/tls.key"`
}

type PersistenceConfig struct {
	WalConfig          wal.Config `yaml:"wal"`
	SnapDir            string     `yaml:"snap_dir"`
	SnapshotIntervalMs int        `yaml:"snapshot_interval"`
}

type ReplicationConfig struct {
}
