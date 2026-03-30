package app

import (
	"fmt"
	"log/slog"

	gossipimpl "github.com/kestfor/in-memorydb/pkg/gossip/gossip"
	membershipv1 "github.com/kestfor/in-memorydb/pkg/membership/v1"
	"github.com/kestfor/in-memorydb/pkg/storage"
	enginev1 "github.com/kestfor/in-memorydb/pkg/storage/engine/v1"
	bufferv3 "github.com/kestfor/in-memorydb/pkg/storage/updates_buffer/v3"
	vmv2 "github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/storage/wal/noop"
	"github.com/kestfor/in-memorydb/pkg/tlsx"
	walv2 "github.com/kestfor/in-memorydb/pkg/storage/wal/v2"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func BuildSubsystems(cfg *storage.Config) (*storage.Subsystems, error) {
	eng := enginev1.NewEngine(enginev1.WithNodeID(cfg.Node.ID))
	vm := vmv2.NewVersionManager(cfg.Node.ID, eng)

	dialOpts, err := buildDialOpts(cfg)
	if err != nil {
		return nil, err
	}
	transport := grpc.NewGRPCTransport(&cfg.Transport, dialOpts...)

	members, err := membershipv1.New(storage.GlobalCfg2Mem(cfg))
	if err != nil {
		return nil, err
	}

	var writeLog wal.WAL
	if cfg.Persistence.Enabled {
		writeLog, err = walv2.New(cfg.Persistence.WalConfig)
		if err != nil {
			return nil, err
		}
	} else {
		writeLog = noop.NewNoopWAL()
	}

	serverOpts, err := buildServerOpts(cfg)
	if err != nil {
		return nil, err
	}

	buffer := bufferv3.NewUpdatesBuffer(1000) // TODO: move to config
	goss := gossipimpl.NewDefaultGossip(&cfg.Gossip, transport, members, vm, writeLog, buffer, serverOpts...)

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

func buildServerOpts(cfg *storage.Config) ([]googlegrpc.ServerOption, error) {
	if cfg.Security.Mode == tlsx.Disabled {
		slog.Info("node-security disabled: Insecure gRPC server will be used")
		return nil, nil
	}

	creds, err := tlsx.LoadServerCredentials(cfg.Security.CaCert, cfg.Security.Cert, cfg.Security.Key)
	if err != nil {
		return nil, fmt.Errorf("build subsystems: load server TLS credentials: %w", err)
	}
	slog.Info("node-security enabled: Mutual TLS will be used for gRPC server")
	return []googlegrpc.ServerOption{googlegrpc.Creds(creds)}, nil
}

func buildDialOpts(cfg *storage.Config) ([]googlegrpc.DialOption, error) {
	if cfg.Security.Mode == tlsx.Disabled {
		slog.Info("node-security disabled: Insecure gRPC transport will be used")
		return []googlegrpc.DialOption{googlegrpc.WithTransportCredentials(insecure.NewCredentials())}, nil
	}

	creds, err := tlsx.LoadClientCredentials(cfg.Security.CaCert, cfg.Security.Cert, cfg.Security.Key)
	if err != nil {
		return nil, fmt.Errorf("build subsystems: load client TLS credentials: %w", err)
	}
	slog.Info("node-security enabled: Mutual TLS will be used for gRPC transport")
	return []googlegrpc.DialOption{googlegrpc.WithTransportCredentials(creds)}, nil
}
