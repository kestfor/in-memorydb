package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	lume "github.com/kestfor/in-memorydb/api/lume"
	configpkg "github.com/kestfor/in-memorydb/pkg/configx/v2"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	config "github.com/kestfor/in-memorydb/pkg/storage"
	"github.com/kestfor/in-memorydb/pkg/utils/logging"
	"google.golang.org/grpc"
)

func Run(ctx context.Context, configPath *string) {
	if configPath == nil || *configPath == "" {
		slog.Error("app.Run: specify a config file path")
		os.Exit(1)
	}

	var cfg config.Config
	if err := configpkg.Load(*configPath, &cfg); err != nil {
		slog.Error("app.Run: load config file error:", "err", err)
		os.Exit(1)
	}

	logging.InitDefault(cfg.Node.ID)

	slog.Info("app.Run: config file loaded successfully", "cfg", cfg)
	mockServer := NewMockServer()

	grpcServer := grpc.NewServer(
		grpc.MaxConcurrentStreams(2048),
		grpc.ChainUnaryInterceptor(
			tracing.UnaryPanicRecoveryInterceptor(),
			tracing.UnaryServerInterceptor(),
		))

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Node.BindAddress, cfg.Node.Port))
	if err != nil {
		slog.Error("app.Run: listen error", "err", err)
		os.Exit(1)
	}

	lume.RegisterLumeServer(grpcServer, mockServer)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("app.Run: grpc server start error", "err", err)
			os.Exit(1)
		}
	}()

	exit := make(chan os.Signal, 1)

	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)

	select {
	case <-exit:
		slog.Info("app.Run: shutting down node server")

		grpcServer.GracefulStop()
	}
}
