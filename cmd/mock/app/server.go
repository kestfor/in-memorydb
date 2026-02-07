package app

import (
	"context"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/kestfor/in-memorydb/api/lume"
)

type MockServer struct {
	lume.UnimplementedLumeServer
}

func NewMockServer() *MockServer {
	return &MockServer{}
}

func (n *MockServer) Set(_ context.Context, _ *lume.SetRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}

func (n *MockServer) Get(_ context.Context, _ *lume.GetRequest) (*lume.GetResponse, error) {
	return &lume.GetResponse{}, nil
}

func (n *MockServer) Delete(_ context.Context, _ *lume.DeleteRequest) (*lume.DeleteResponse, error) {
	return &lume.DeleteResponse{}, nil
}

func (n *MockServer) Apply(_ context.Context, _ *lume.ApplyRequest) (*empty.Empty, error) {
	return &empty.Empty{}, nil
}
