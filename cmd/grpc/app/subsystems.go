package app

import (
	gossipimpl "github.com/kestfor/in-memorydb/pkg/gossip/gossip"
	membershipv1 "github.com/kestfor/in-memorydb/pkg/membership/v1"
	"github.com/kestfor/in-memorydb/pkg/storage"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	bufferimpl "github.com/kestfor/in-memorydb/pkg/storage/updates_buffer/v2"
	vmv2 "github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/storage/wal/noop"
	walv1 "github.com/kestfor/in-memorydb/pkg/storage/wal/v1"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc"
)

func BuildSubsystems(cfg *storage.Config) (*storage.Subsystems, error) {
	eng := enginev1.NewEngine(enginev1.WithNodeID(cfg.Node.ID))
	vm := vmv2.NewVersionManager(cfg.Node.ID, eng)
	transport := grpc.NewGRPCTransport(&cfg.Transport)

	members, err := membershipv1.New(storage.GlobalCfg2Mem(cfg))
	if err != nil {
		return nil, err
	}

	var writeLog wal.WAL
	if cfg.Persistence.Enabled {
		writeLog, err = walv1.New(cfg.Persistence.WalConfig)
		if err != nil {
			return nil, err
		}
	} else {
		writeLog = noop.NewNoopWAL()
	}

	buffer := bufferimpl.NewBuffer(1000) // TODO: move to config
	goss := gossipimpl.NewDefaultGossip(&cfg.Gossip, transport, members, vm, writeLog, buffer)

	return &storage.Subsystems{
		Engine:         eng,
		VersionManager: vm,
		Transport:      transport,
		Membership:     members,
		WAL:            writeLog,
		UpdatesBuffer:  buffer,
		Gossip:         goss,
	}, nil
}
