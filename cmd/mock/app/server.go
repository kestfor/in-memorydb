package app

import (
	"context"

	lume "github.com/kestfor/in-memorydb/api/lume"
	"google.golang.org/protobuf/types/known/emptypb"
)

type MockServer struct {
	lume.UnimplementedLumeServer
}

func NewMockServer() *MockServer {
	return &MockServer{}
}

var (
	emptyResp = &emptypb.Empty{}
	getResp   = &lume.GetResponse{}
	delResp   = &lume.DeleteResponse{}
)

func (n *MockServer) Set(_ context.Context, _ *lume.SetRequest) (*emptypb.Empty, error) {
	return emptyResp, nil
}

func (n *MockServer) Get(_ context.Context, _ *lume.GetRequest) (*lume.GetResponse, error) {
	return getResp, nil
}

func (n *MockServer) Delete(_ context.Context, _ *lume.DeleteRequest) (*lume.DeleteResponse, error) {
	return delResp, nil
}

func (n *MockServer) Apply(_ context.Context, _ *lume.ApplyRequest) (*emptypb.Empty, error) {
	return emptyResp, nil
}
