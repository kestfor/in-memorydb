package storage

import (
	"github.com/kestfor/in-memorydb/pkg/gossip"
	"github.com/kestfor/in-memorydb/pkg/membership"
	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	"github.com/kestfor/in-memorydb/pkg/storage/updates_buffer"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/transport"
)

type Subsystems struct {
	Engine         engine.Engine
	VersionManager version_manager.VersionManager
	Transport      transport.Transport
	Membership     membership.Membership
	WAL            wal.WAL
	UpdatesBuffer  updates_buffer.UpdatesBuffer
	Gossip         gossip.Gossip
}
