package app

import (
	"context"
	"fmt"
	"github.com/kestfor/in-memorydb/api/lumepb"
	configpkg "github.com/kestfor/in-memorydb/pkg/configx/v2"
	"github.com/kestfor/in-memorydb/pkg/observability"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	config "github.com/kestfor/in-memorydb/pkg/storage"
	"github.com/kestfor/in-memorydb/pkg/utils/logging"
	"log/slog"
	"net"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
)

func Run(ctx context.Context, configPath *string) {
	if configPath == nil || *configPath == "" {
		slog.Error("app.Run: specify a config file path")
		os.Exit(1)
	}

	//go func() {
	//	http.ListenAndServe("localhost:6060", nil)
	//}()

	var cfg config.Config
	if err := configpkg.Load(*configPath, &cfg); err != nil {
		slog.Error("app.Run: load config file error:", "err", err)
		os.Exit(1)
	}

	logging.InitDefault(cfg.Node.ID)

	slog.Info("app.Run: config file loaded successfully", "cfg", cfg)

	nodeServer, err := NewNodeServer(&cfg)
	if err != nil {
		slog.Error("app.Run: create node server error", "err", err)
		os.Exit(1)
	}

	err = nodeServer.StartStorage(ctx)
	if err != nil {
		slog.Error("app.Run: start storage error", "err", err)
		os.Exit(1)
	}

	if cfg.TraceConfig.Enable {
		err = observability.InitTracerWithEndpoint(ctx, cfg.TraceConfig.Endpoint)
		if err != nil {
			slog.Error("app.Run: init tracer error", "err", err)
		}
		slog.Info("app.Run: init tracer successfully")
	}

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Node.BindAddress, cfg.Node.Port))
	if err != nil {
		slog.Error("app.Run: listen error", "err", err)
		os.Exit(1)
	}
	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		tracing.UnaryPanicRecoveryInterceptor(),
		tracing.UnaryServerInterceptor(),
	))
	lumepb.RegisterLumeServer(grpcServer, nodeServer)

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
		_ = nodeServer.GracefulStopStorage()
	}

}
