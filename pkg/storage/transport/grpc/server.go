package grpc

import (
	"in-memorydb/pkg/storage/transport/grpc/transportpb"

	"google.golang.org/grpc"
)

// должен находиться почти на самом верхнем уровне так как ему нужен доступ к version manager, delta buffer, engine

type updatesServer struct {
	transportpb.UnimplementedUpdatesServer
}

func RegisterUpdatesService(server *grpc.Server) {
	transportpb.RegisterUpdatesServer(server, &updatesServer{})
}
