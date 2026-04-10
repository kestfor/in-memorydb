package grpc

import (
	"context"
	"log/slog"
	"sync"

	"github.com/kestfor/in-memorydb/pkg/structs"
	transportpb2 "github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"github.com/kestfor/in-memorydb/pkg/types"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/protobuf/types/known/emptypb"
)

type TransportConfig struct {
	MaxMessageSize int `yaml:"max_message_size" env:"TRANSPORT_MAX_MESSAGE_SIZE" default:"1073741824"`
	MaxBatchSize   int `yaml:"max_batch_size" env:"TRANSPORT_MAX_BATCH_SIZE" default:"10000"`
}

type ClientPool struct {
	mu             sync.Mutex
	clients        map[string]*grpc.ClientConn
	dialOpts       []grpc.DialOption
	maxMessageSize int
}

type GRPCTransport struct {
	config *TransportConfig
	pool   *ClientPool
}

func NewClientPool(maxMessageSize int, dialOpts ...grpc.DialOption) *ClientPool {
	return &ClientPool{
		clients:        make(map[string]*grpc.ClientConn),
		dialOpts:       dialOpts,
		maxMessageSize: maxMessageSize,
	}
}

func (p *ClientPool) GetClient(peer string, addr string) (transportpb2.UpdatesClient, error) {
	p.mu.Lock()
	conn, ok := p.clients[peer]
	if ok {
		state := conn.GetState()
		if state == connectivity.Ready || state == connectivity.Idle || state == connectivity.Connecting {
			p.mu.Unlock()
			return transportpb2.NewUpdatesClient(conn), nil
		}
		delete(p.clients, peer)
	}
	p.mu.Unlock()

	opts := make([]grpc.DialOption, len(p.dialOpts))
	copy(opts, p.dialOpts)
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(p.maxMessageSize)))
	newConn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	// Check again in case another goroutine already created a new connection.
	if existing, ok := p.clients[peer]; ok {
		p.mu.Unlock()
		_ = newConn.Close()
		return transportpb2.NewUpdatesClient(existing), nil
	}
	p.clients[peer] = newConn
	p.mu.Unlock()

	return transportpb2.NewUpdatesClient(newConn), nil
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

func NewGRPCTransport(config *TransportConfig, dialOpts ...grpc.DialOption) *GRPCTransport {
	return &GRPCTransport{
		config: config,
		pool:   NewClientPool(config.MaxMessageSize, dialOpts...),
	}
}

func (t *GRPCTransport) Send(ctx context.Context, addr string, updates []types.Update) error {
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

func (t *GRPCTransport) Pull(ctx context.Context, addr string, versions map[string][]structs.Range) ([]types.Update, error) {
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
