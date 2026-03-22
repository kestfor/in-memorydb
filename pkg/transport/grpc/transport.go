package grpc

import (
	"context"
	"github.com/kestfor/in-memorydb/pkg/structs"
	transportpb2 "github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"github.com/kestfor/in-memorydb/pkg/types"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type TransportConfig struct{}

type ClientPool struct {
	mu      sync.Mutex
	clients map[string]*grpc.ClientConn
}

type GRPCTransport struct {
	pool *ClientPool
}

func NewClientPool() *ClientPool {
	return &ClientPool{
		clients: make(map[string]*grpc.ClientConn),
	}
}

func (p *ClientPool) GetClient(peer string, addr string) (transportpb2.UpdatesClient, error) {
	p.mu.Lock()
	conn, ok := p.clients[peer]
	p.mu.Unlock()

	if ok {
		if conn.GetState() == connectivity.Ready {
			return transportpb2.NewUpdatesClient(conn), nil
		}

		if err := conn.Close(); err != nil {
			slog.Error("grpc.Transport: Failed to close client connection", "peer", peer, "addr", addr, "err", err)
		}
	}

	maxSize := 1024 * 1024 * 1024 // TODO
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxSize)))
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.clients[peer] = conn
	p.mu.Unlock()

	return transportpb2.NewUpdatesClient(conn), nil
}

func (p *ClientPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.clients {
		if err := conn.Close(); err != nil {
			slog.Error("grpc.Transport: Failed to close client connection", "peer", conn.GetState(), "err", err)
		}
	}
	p.clients = make(map[string]*grpc.ClientConn)
}

func NewGRPCTransport(config *TransportConfig) *GRPCTransport {
	return &GRPCTransport{
		pool: NewClientPool(),
	}
}

func (t *GRPCTransport) Send(ctx context.Context, addr string, updates []*types.Update) error {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return err
	}

	upd, err := fromDomainUpdates(updates)
	if err != nil {
		return err
	}

	_, err = client.Publish(ctx, &transportpb2.PublishRequest{Updates: upd})
	return err
}

func (t *GRPCTransport) Pull(ctx context.Context, addr string, versions map[string][]structs.Range) ([]*types.Update, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	res, err := client.Get(ctx, &transportpb2.GetRequest{Versions: fromDomainVersions(versions)})
	if err != nil {
		return nil, err
	}
	return toDomainUpdates(res.Updates)
}

func (t *GRPCTransport) GetVersion(ctx context.Context, addr string) (map[string]uint64, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetVersionVector(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	return resp.VectorClock, nil
}

func (t *GRPCTransport) GetKeyDigests(ctx context.Context, addr string, bucket uint32) (map[string]uint64, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetKeyDigests(ctx, &transportpb2.GetKeyDigestsRequest{Bucket: bucket})
	if err != nil {
		return nil, err
	}
	return resp.GetDigests(), nil
}

func (t *GRPCTransport) PullKeyStates(ctx context.Context, addr string, keys []string) ([]*types.KeyState, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	resp, err := client.PullKeyStates(ctx, &transportpb2.PullKeyStatesRequest{Keys: keys})
	if err != nil {
		return nil, err
	}

	return toKeyStates(resp.GetKeyStates()), nil
}
