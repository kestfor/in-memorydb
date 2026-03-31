package storage

import (
	"time"

	"github.com/kestfor/in-memorydb/pkg/gossip/gossip"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	walv2 "github.com/kestfor/in-memorydb/pkg/storage/wal/v2"
	"github.com/kestfor/in-memorydb/pkg/tlsx"
	transport "github.com/kestfor/in-memorydb/pkg/transport/grpc"
)

type Config struct {
	Node        NodeConfig                `yaml:"node"`
	Gossip      gossip.Config             `yaml:"gossip"`
	Membership  MembershipConfig          `yaml:"membership"`
	Seeds       []string                  `yaml:"seeds"`
	Persistence PersistenceConfig         `yaml:"persistence"`
	Security    tlsx.SecurityConfig       `yaml:"security"`
	Transport   transport.TransportConfig `yaml:"transport"`
	TraceConfig TraceConfig               `yaml:"trace"`
	Engine      enginev1.EngineConfig     `yaml:"engine"`
	Buffer      BufferConfig              `yaml:"buffer"`
}

type TraceConfig struct {
	Endpoint string `yaml:"endpoint" env:"TRACE_ENDPOINT" required:"true" default:"localhost:4318"`
	Enabled  bool   `yaml:"enabled" env:"TRACE_ENABLED" default:"false"`
}

type NodeConfig struct {
	ID                   string `yaml:"id" env:"NODE_ID" required:"true"`
	BindAddress          string `yaml:"bind_address" env:"NODE_BIND_ADDRESS" required:"true" default:"0.0.0.0"`
	Port                 uint16 `yaml:"port" env:"NODE_PORT" required:"true" default:"8080"`
	MaxConcurrentStreams uint32 `yaml:"max_concurrent_streams" env:"NODE_MAX_CONCURRENT_STREAMS" default:"2048"`
}

type MembershipConfig struct {
	AdvertiseAddr string `yaml:"advertise_addr" env:"MEMBERSHIP_ADVERTISE_ADDR"`
	Port          uint16 `yaml:"port" env:"MEMBERSHIP_PORT" required:"true" default:"8082"`
}

type PersistenceConfig struct {
	Enabled   bool         `yaml:"enabled" default:"true"`
	WalConfig walv2.Config `yaml:"wal"`
}

type ReplicationConfig struct {
}

type BufferConfig struct {
	Size          int           `yaml:"size" env:"BUFFER_SIZE" default:"1000"`
	ReadInterval  time.Duration `yaml:"read_interval" env:"BUFFER_READ_INTERVAL" default:"5s"`
	PeekBatchSize int           `yaml:"peek_batch_size" env:"BUFFER_PEEK_BATCH_SIZE" default:"100"`
}
