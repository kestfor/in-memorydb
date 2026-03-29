package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"

	lume "github.com/kestfor/in-memorydb/api/lume"
	configpkg "github.com/kestfor/in-memorydb/pkg/configx/v2"
	"github.com/kestfor/in-memorydb/pkg/observability"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	config "github.com/kestfor/in-memorydb/pkg/storage"
	"github.com/kestfor/in-memorydb/pkg/tlsx"
	"github.com/kestfor/in-memorydb/pkg/utils/logging"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

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

	subs, err := BuildSubsystems(&cfg)
	if err != nil {
		slog.Error("app.Run: build subsystems error", "err", err)
		os.Exit(1)
	}

	nodeServer := NewNodeServer(&cfg, subs)

	err = nodeServer.StartStorage(ctx)
	if err != nil {
		slog.Error("app.Run: start storage error", "err", err)
		os.Exit(1)
	}

	if cfg.TraceConfig.Enabled {
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
	serverOpts := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(2048),
		grpc.ChainUnaryInterceptor(
			tracing.UnaryPanicRecoveryInterceptor(),
			tracing.UnaryServerInterceptor(),
		),
	}

	if cfg.Security.Mode == tlsx.Full {
		creds, err := tlsx.LoadServerCredentials(cfg.Security.CaCert, cfg.Security.Cert, cfg.Security.Key)
		if err != nil {
			slog.Error("app.Run: load server TLS credentials error", "err", err)
			os.Exit(1)
		}
		serverOpts = append(serverOpts, grpc.Creds(creds))
		slog.Info("node-security: client API TLS enabled (mutual TLS)")
	} else {
		slog.Info("node-security: client API TLS disabled (insecure mode)")
	}

	grpcServer := grpc.NewServer(serverOpts...)
	lume.RegisterLumeServer(grpcServer, nodeServer)
	wireHealthCheck(grpcServer)

	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("app.Run: grpc server start error", "err", err)
			os.Exit(1)
		}
	}()

	exit := make(chan os.Signal, 1)

	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)

	<-exit

	slog.Info("app.Run: shutting down node server")

	grpcServer.GracefulStop()
	_ = nodeServer.GracefulStopStorage()

}

func wireHealthCheck(server *grpc.Server) {
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
}
