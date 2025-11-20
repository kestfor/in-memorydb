package grpc

import (
	"context"
	"in-memorydb/pkg/storage"
	"in-memorydb/pkg/storage/transport/grpc/transportpb"
	"log/slog"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

type TransportConfig struct {
}

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

func (p *ClientPool) GetClient(peer string, addr string) (transportpb.UpdatesClient, error) {
	p.mu.Lock()
	conn, ok := p.clients[peer]
	p.mu.Unlock()

	if ok {
		if conn.GetState() == connectivity.Ready {
			return transportpb.NewUpdatesClient(conn), nil
		}

		if err := conn.Close(); err != nil {
			slog.Error("Failed to close client connection", "peer", peer, "addr", addr, "err", err)
		}
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.clients[peer] = conn
	p.mu.Unlock()

	return transportpb.NewUpdatesClient(conn), nil
}

func (p *ClientPool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.clients {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close client connection", "peer", conn.GetState(), "err", err)
		}
	}
	p.clients = make(map[string]*grpc.ClientConn)
}

func NewGRPCTransport(config *TransportConfig) *GRPCTransport {
	return &GRPCTransport{
		pool: NewClientPool(),
	}
}

func (t *GRPCTransport) Send(ctx context.Context, addr string, updates []*storage.Update) error {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return err
	}

	upd, err := fromDomainUpdates(updates)
	if err != nil {
		return err
	}

	_, err = client.Publish(ctx, &transportpb.PublishRequest{Updates: upd})
	return err
}

func (t *GRPCTransport) Pull(ctx context.Context, addr string, version storage.Version) ([]*storage.Update, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	res, err := client.Get(ctx, &transportpb.GetRequest{Version: fromDomainVersion(version)})
	if err != nil {
		return nil, err
	}
	return toDomainUpdates(res.Updates)
}

func (t *GRPCTransport) GetVersion(ctx context.Context, addr string) (storage.Version, error) {
	client, err := t.pool.GetClient(addr, addr)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetVersionVector(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	return toDomainVersion(resp.Version), nil
}
