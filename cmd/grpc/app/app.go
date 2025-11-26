package app

import (
	"context"
	"fmt"
	"in-memorydb/api/lumepb"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/utils/logging"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
)

func Run(ctx context.Context, configPath *string) {
	if configPath == nil || *configPath == "" {
		slog.Error("specify a config file path")
		os.Exit(1)
	}

	cfg, err := config.Read(*configPath)
	if err != nil {
		slog.Error("load config file error:", err)
		os.Exit(1)
	}

	logging.InitDefault(cfg.Node.ID)

	slog.Info("config file loaded successfully", "cfg", cfg)

	nodeServer, err := NewNodeServer(cfg)
	if err != nil {
		slog.Error("create node server error:", err)
		os.Exit(1)
	}

	err = nodeServer.StartStorage(ctx)
	if err != nil {
		slog.Error("start storage error:", err)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Node.BindAddress, cfg.Node.Port))
	if err != nil {
		slog.Error("listen error:", err)
		os.Exit(1)
	}
	grpcServer := grpc.NewServer()
	lumepb.RegisterLumeServer(grpcServer, nodeServer)

	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("grpc server start error:", err)
	}

}
