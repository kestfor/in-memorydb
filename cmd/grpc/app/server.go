package app

import (
	"context"
	"fmt"
	"log/slog"

	lume "github.com/kestfor/in-memorydb/api/lume"
	"github.com/kestfor/in-memorydb/pkg/storage"

	"github.com/golang/protobuf/ptypes/empty"
)

type NodeServer struct {
	lume.UnimplementedLumeServer
	storage *storage.Storage
}

func NewNodeServer(config *storage.Config, subs *storage.Subsystems) *NodeServer {
	st := storage.NewStorage(config, subs)
	return &NodeServer{storage: st}
}

func (n *NodeServer) StartStorage(ctx context.Context) error {
	return n.storage.StartUp(ctx)
}

func (n *NodeServer) GracefulStopStorage() error {
	return n.storage.GracefulStop()
}

func (n *NodeServer) Set(ctx context.Context, request *lume.SetRequest) (*empty.Empty, error) {
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

func (n *NodeServer) Get(ctx context.Context, request *lume.GetRequest) (*lume.GetResponse, error) {
	val, typ, ok := n.storage.Get(ctx, request.GetKey())

	res, err := toGetResponse(val, typ, ok)

	if err != nil {
		slog.ErrorContext(ctx, "node.Get: Error while getting key", "err", err)
	} else {
		slog.InfoContext(ctx, "node.Get: Successfully get key", "key", request.GetKey(), "value", fmt.Sprintf("%v", val))
	}

	return res, err
}

func (n *NodeServer) Delete(ctx context.Context, request *lume.DeleteRequest) (*lume.DeleteResponse, error) {
	ok, err := n.storage.Delete(ctx, request.GetKey())
	if err != nil {
		slog.ErrorContext(ctx, "node.Delete: Error while deleting key", "err", err)
		return nil, err
	}

	if ok {
		slog.InfoContext(ctx, "node.Delete: Successfully delete key", "key", request.GetKey())
	}
	return &lume.DeleteResponse{Ok: ok}, nil
}

func (n *NodeServer) Apply(ctx context.Context, request *lume.ApplyRequest) (*empty.Empty, error) {
	switch op := request.GetOperation().(type) {
	case *lume.ApplyRequest_CounterOperationInc:
		_, err := n.storage.ApplyInc(ctx, request.Key, op.CounterOperationInc.Val)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while incrementing counter", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	case *lume.ApplyRequest_CounterOperationDec:
		_, err := n.storage.ApplyDec(ctx, request.Key, op.CounterOperationDec.Val)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while decrementing counter", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	case *lume.ApplyRequest_RegisterOperation:
		_, err := n.storage.ApplySetRegister(ctx, request.Key, op.RegisterOperation.Value)
		if err != nil {
			slog.ErrorContext(ctx, "node.Do: Error while registering operation", "err", err, "key", request.Key)
		}
		return &empty.Empty{}, err
	default:
		return nil, fmt.Errorf("unknown operation type: %T", op)
	}
}
