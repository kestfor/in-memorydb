package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/kestfor/in-memorydb/api/lume"
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
		// Увеличиваем параллелизм для обработки большого количества клиентов
		grpc.MaxConcurrentStreams(10000), // было 2048
		// Увеличиваем window sizes для flow control
		grpc.InitialWindowSize(1<<20),     // 1MB per stream
		grpc.InitialConnWindowSize(1<<21), // 2MB for connection
		// Увеличиваем буферы для I/O
		grpc.WriteBufferSize(512*1024), // 512KB write buffer
		grpc.ReadBufferSize(512*1024),  // 512KB read buffer
		// Настройка worker pool для обработки запросов
		grpc.NumStreamWorkers(100), // увеличиваем количество stream workers
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
