package storage

import (
	"github.com/kestfor/in-memorydb/pkg/gossip/gossip"
	"github.com/kestfor/in-memorydb/pkg/storage/wal/v1"
	"github.com/kestfor/in-memorydb/pkg/tlsx"
	transport "github.com/kestfor/in-memorydb/pkg/transport/grpc"
)

type Config struct {
	Node        NodeConfig                `yaml:"node"`
	Gossip      gossip.Config             `yaml:"gossip"`
	Membership  MembershipConfig          `yaml:"membership"`
	Seeds       []string                  `yaml:"seeds"`
	Persistence PersistenceConfig         `yaml:"persistence"`
	Replication ReplicationConfig         `yaml:"replication"`
	Security    tlsx.SecurityConfig       `yaml:"security"`
	Transport   transport.TransportConfig `yaml:"transport"`
	TraceConfig TraceConfig               `yaml:"trace"`
}

type TraceConfig struct {
	Endpoint string `yaml:"endpoint" env:"TRACE_ENDPOINT" required:"true" default:"localhost:4318"`
	Enabled  bool   `yaml:"enabled" env:"TRACE_ENABLED" default:"false"`
}

type NodeConfig struct {
	ID          string `yaml:"id" env:"NODE_ID" required:"true"`
	BindAddress string `yaml:"bind_address" env:"NODE_BIND_ADDRESS" required:"true" default:"0.0.0.0"`
	Port        uint16 `yaml:"port" env:"NODE_PORT" required:"true" default:"50051"`
}

type MembershipConfig struct {
	Port uint16 `yaml:"port" env:"MEMBERSHIP_PORT" required:"true" default:"50053"`
}

type PersistenceConfig struct {
	Enabled   bool       `yaml:"enabled" default:"true"`
	WalConfig wal.Config `yaml:"wal"`
}

type ReplicationConfig struct {
}
