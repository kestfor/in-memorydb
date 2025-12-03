package app

import (
	"context"
	"fmt"
	"in-memorydb/api/lumepb"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage"
	"log/slog"

	"github.com/golang/protobuf/ptypes/empty"
)

var factory = crdt.NewFabric()

type NodeServer struct {
	lumepb.UnimplementedLumeServer

	storage *storage.Storage
}

func NewNodeServer(config *config.Config) (*NodeServer, error) {
	st, err := storage.NewStorage(config)
	if err != nil {
		return nil, err
	}
	return &NodeServer{storage: st}, nil
}

func (n *NodeServer) StartStorage(ctx context.Context) error {
	return n.storage.StartUp(ctx)
}

func (n *NodeServer) GracefulStopStorage() error {
	return n.storage.GracefulStop()
}

func (n *NodeServer) Set(ctx context.Context, request *lumepb.SetRequest) (*empty.Empty, error) {
	domainType, err := toDomainCRDTType(request.GetCrdtType())
	if err != nil {
		return nil, err
	}

	err = n.storage.Put(ctx, request.GetKey(), domainType)
	if err != nil {
		return nil, err
	}

	slog.InfoContext(ctx, "node.Set: Successfully set key", "key", request.GetKey())

	return &empty.Empty{}, nil
}

func (n *NodeServer) Get(ctx context.Context, request *lumepb.GetRequest) (*lumepb.GetResponse, error) {
	val, typ, ok := n.storage.Get(ctx, request.GetKey())

	res, err := toGetResponse(val, typ, ok)

	if err != nil {
		slog.ErrorContext(ctx, "node.Get: Error while getting key", "err", err)
	} else {
		slog.InfoContext(ctx, "node.Get: Successfully get key", "key", request.GetKey(), "value", fmt.Sprintf("%v", val))
	}

	return res, err
}

func (n *NodeServer) Delete(ctx context.Context, request *lumepb.DeleteRequest) (*lumepb.DeleteResponse, error) {
	ok, err := n.storage.Delete(ctx, request.GetKey())
	if err != nil {
		slog.ErrorContext(ctx, "node.Delete: Error while deleting key", "err", err)
		return nil, err
	}

	if ok {
		slog.InfoContext(ctx, "node.Delete: Successfully delete key", "key", request.GetKey())
	}
	return &lumepb.DeleteResponse{Ok: ok}, nil
}

func (n *NodeServer) Apply(ctx context.Context, request *lumepb.ApplyRequest) (*empty.Empty, error) {
	switch op := request.GetOperation().(type) {
	case *lumepb.ApplyRequest_CounterOperationInc:
		_, err := n.storage.ApplyInc(ctx, request.Key, op.CounterOperationInc.Val)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while incrementing counter", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	case *lumepb.ApplyRequest_CounterOperationDec:
		_, err := n.storage.ApplyDec(ctx, request.Key, op.CounterOperationDec.Val)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while decrementing counter", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	case *lumepb.ApplyRequest_RegisterOperation:
		_, err := n.storage.ApplySetRegister(ctx, request.Key, op.RegisterOperation.Value)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while registering operation", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	default:
		return nil, fmt.Errorf("unknown operation type: %T", op)
	}
}
