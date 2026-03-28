package gossip

import (
	"context"
	"errors"
	"fmt"
	"github.com/kestfor/in-memorydb/pkg/gossip"
	"github.com/kestfor/in-memorydb/pkg/gossip/gossip_buffer"
	"github.com/kestfor/in-memorydb/pkg/membership"
	"github.com/kestfor/in-memorydb/pkg/observability/spans"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	buffer "github.com/kestfor/in-memorydb/pkg/storage/updates_buffer"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/transport"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"github.com/kestfor/in-memorydb/pkg/types"
	"log/slog"
	"math/rand/v2"
	"net"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpcserver "google.golang.org/grpc"
)

const maxSendNum = 10000

type Config struct {
	AdvertiseAddress      string `yaml:"bind_address" env:"GOSSIP_BIND_ADDRESS" required:"true"`
	Port                  uint16 `yaml:"port" env:"GOSSIP_PORT" default:"50052" required:"true"`
	Protocol              string `yaml:"protocol" env:"GOSSIP_PROTOCOL" default:"SWIM"`
	AntiEntropyIntervalMs int    `yaml:"interval" env:"GOSSIP_ANT_ENTROPY_INTERVAL" default:"5000"`
	Fanout                int    `yaml:"fanout" env:"GOSSIP_FANOUT" default:"3"`
	Retries               int    `yaml:"retries" env:"GOSSIP_RETRIES" default:"3"`
}

var ErrNoPeers = errors.New("no peers available")

// DefaultGossip implements both client and server for gossip updates.
// Implementation does 2 types of updates distribution
// 1) Impl periodically reads updates channel from Start() function and sends new added updates to other peers
// 2) Impl periodically runs the anti-entropy process by requesting random peers version vector and pulling missing updates
// Implementation consists of both client and server.
// Server answers to incoming other clients messages
// Client calls other servers to receive updates
type DefaultGossip struct {
	config         *Config
	memberlist     membership.Membership
	transport      transport.Transport
	versionManager version_manager.VersionManager
	buffer         buffer.UpdatesBuffer // local updates buffer

	gbuffer    *gossip_buffer.GossipBuffer // buffer used for epidemic data sending
	wal        wal.WAL
	serverOpts []grpcserver.ServerOption

	updatesChannel chan []*types.Update // channel for clients updates, periodically reads and send data from this channel to other peers
	shutdown       context.CancelFunc   // shutdown is a function to cancel the context, used to trigger graceful shutdown of ongoing processes.
}

func NewDefaultGossip(config *Config, transport transport.Transport, list membership.Membership, manager version_manager.VersionManager, wal wal.WAL, buffer buffer.UpdatesBuffer, serverOpts ...grpcserver.ServerOption) *DefaultGossip {
	return &DefaultGossip{
		config:         config,
		transport:      transport,
		memberlist:     list,
		versionManager: manager,
		wal:            wal,
		buffer:         buffer,
		serverOpts:     serverOpts,
		gbuffer:        gossip_buffer.NewGossipBuffer(5000), // TODO настроить размер
		updatesChannel: make(chan []*types.Update, 10),      // TODO настроить размер
	}
}

// Start initializes and starts the gossip process, returning a channel for updates and an error, if any occurs.
// It also registers grpc server for update exchange between peers
func (g *DefaultGossip) Start(ctx context.Context) (chan<- []*types.Update, error) {
	ctx, cancel := context.WithCancel(ctx)
	g.shutdown = cancel

	if err := g.listenUpdates(ctx); err != nil {
		return nil, err
	}

	go g.runAntiEntropy(ctx)
	go g.readUpdates(ctx)

	return g.updatesChannel, nil
}

func (g *DefaultGossip) Shutdown() error {
	g.shutdown()
	time.Sleep(time.Second)
	return nil
}

func (g *DefaultGossip) Send(ctx context.Context, data []*types.Update) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanGossipSend, trace.WithAttributes(attribute.Int("fanout", g.config.Fanout)))
	defer span.End()

	peers := filterOutSelf(g.memberlist.Members(), g.memberlist.LocalNode())
	picked := structs.NewSet[int]()
	fanout := min(len(peers), g.config.Fanout)
	successNum := 0

	for successNum < fanout {
		if len(picked) == len(peers) {
			return tracing.RecordError(ctx, fmt.Errorf("too few active peers"))
		}

		ind := rand.IntN(len(peers))
		if picked.Contains(ind) {
			continue
		}
		peer := peers[ind]

		for _ = range g.config.Retries {
			err := g.transport.Send(ctx, peer.GossipAddr().String(), data)
			if err != nil {
				slog.Warn("gossip.Send: error while sending data to peer", "peer", peer.GossipAddr().String(), "err", err)
				continue
			}
			successNum++
			break
		}

		picked.Add(ind)
	}

	span.SetAttributes(attribute.Int("peers_contacted", successNum))
	span.SetStatus(codes.Ok, "")
	return nil
}

func (g *DefaultGossip) AsyncSend(ctx context.Context, data []*types.Update) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- g.Send(ctx, data)
		close(ch)
	}()

	return ch
}

// TODO добавить возможность указать конкретного peer
func (g *DefaultGossip) Pull(ctx context.Context, peer types.Node, version map[string][]structs.Range) ([]*types.Update, error) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanGossipPull)
	defer span.End()

	if peer == nil {
		var err error
		peer, err = g.getRandomPeer()
		if err != nil {
			return nil, tracing.RecordError(ctx, err)
		}
	}

	span.SetAttributes(attribute.String("peer", peer.ID()))
	updates, err := g.transport.Pull(ctx, peer.GossipAddr().String(), version)
	if err != nil {
		return nil, tracing.RecordError(ctx, err)
	}

	span.SetStatus(codes.Ok, "")
	return updates, nil
}

func (g *DefaultGossip) GetVersionVector(ctx context.Context, peer types.Node) (*gossip.VersionVectorResponse, error) {
	if peer == nil {
		var err error
		peer, err = g.getRandomPeer()
		if err != nil {
			return nil, err
		}
	}
	version, err := g.transport.GetVersion(ctx, peer.GossipAddr().String())
	if err != nil {
		return nil, err
	}
	return &gossip.VersionVectorResponse{
		NodeID:      peer.ID(),
		VectorClock: version,
	}, nil

}

// readUpdates processes incoming updates from the updatesChannel and sends them asynchronously to peers.
// A semaphore is used to limit the number of concurrently executed send operations.
func (g *DefaultGossip) readUpdates(ctx context.Context) {
	sem := make(chan struct{}, 5) // TODO где MaxConcurrentSends в конфиге
	for updates := range g.updatesChannel {
		sem <- struct{}{}
		go func(u []*types.Update) {
			defer func() { <-sem }()
			ctx, span := tracing.StartSpan(ctx, spans.SpanGossipReadUpdates, trace.WithAttributes(attribute.Int("updates_count", len(u))))
			defer span.End()

			clusterSize := len(g.memberlist.Members())

			for index := range u {
				u[index].TTL = getTTLNumForAsync(clusterSize, g.config.Fanout, g.config.AntiEntropyIntervalMs/1000)
			}

			ttlUpds := g.gbuffer.PeekN(maxSendNum - len(u))
			u = append(u, ttlUpds...)

			if err := g.Send(ctx, u); err != nil {
				slog.Warn("gossip.readUpdates: send failed", "err", err)
				return
			}

			span.SetStatus(codes.Ok, "")
		}(updates)
	}
}

func (g *DefaultGossip) getRandomPeer() (types.Node, error) {
	peers := filterOutSelf(g.memberlist.Members(), g.memberlist.LocalNode())
	if len(peers) == 0 {
		return nil, ErrNoPeers
	}
	ind := rand.IntN(len(peers))
	return peers[ind], nil
}

// runAntiEntropy periodically triggers the anti-entropy process to ensure data consistency across gossip nodes.
func (g *DefaultGossip) runAntiEntropy(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(g.config.AntiEntropyIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "gossip.runAntiEntropy: shutting down anti-entropy")
			return
		case <-ticker.C:
			g.antiEntropyRound(ctx)
		}
	}
}

// antiEntropyRound executes an anti-entropy process round, ensuring consistency by syncing updates with a random peer.
func (g *DefaultGossip) antiEntropyRound(ctx context.Context) {
	ctx, span := tracing.StartSpan(ctx, spans.SpanGossipAntiEntropyRound, trace.WithAttributes(attribute.String("node_id", g.memberlist.LocalNode().ID())))
	defer span.End()

	withTimeOut, cancel := context.WithTimeout(ctx, time.Second*60) // TODO выбрать timeout через конфиг
	defer cancel()

	peer, err := g.getRandomPeer()
	if err != nil {
		slog.Debug("gossip.antiEntropyRound: No need for anti-entropy, node in standalone mode", "err", err)
		return
	}
	span.SetAttributes(attribute.String("peer", peer.ID()))

	received, err := g.GetVersionVector(withTimeOut, peer)
	if err != nil {
		slog.Error("gossip.antiEntropyRound: anti-entropy failed", "err", err)
		return
	}

	diff := g.versionManager.VersionDiff(received.VectorClock)
	if len(diff) == 0 {
		slog.DebugContext(ctx, "gossip.antiEntropyRound: No difference with peer found", "peer_id", peer.ID())
		return
	}

	withTimeOut, cancel = context.WithTimeout(withTimeOut, time.Second*60)
	defer cancel()
	updates, err := g.Pull(withTimeOut, peer, diff)
	if err != nil {
		slog.Error("gossip.antiEntropyRound: Error pulling updates", "err", err)
		return
	}

	slog.InfoContext(ctx, "gossip.antiEntropyRound: Successfully pulled requested updates", "received_num", len(updates), "from_peer", peer.ID())

	if len(updates) > 0 {
		go func() {
			g.versionManager.Update(ctx, updates...)
			for _, upd := range updates {
				err := g.wal.Append(ctx, upd)
				if err != nil {
					slog.Error("gossip.antiEntropyRound: Error appending update to WAL", "err", err)
				}
			}
		}()
	}

	span.SetStatus(codes.Ok, "")
}

// listenUpdates starts a gRPC server to handle gossip updates and listens on the configured address and port.
// It registers the updates server, begins serving requests, and monitors the context for shutdown signals.
// Returns an error if the server fails to start or encounters issues during execution.
func (g *DefaultGossip) listenUpdates(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", g.config.AdvertiseAddress, g.config.Port))
	if err != nil {
		return fmt.Errorf("cannot listen updates on '%s:%d': %w", g.config.AdvertiseAddress, g.config.Port, err)
	}

	updatesServer := grpc.NewUpdatesServer(g.buffer, g.gbuffer, g.wal, g.versionManager)
	opts := append(g.serverOpts, grpcserver.ChainUnaryInterceptor(
		tracing.UnaryPanicRecoveryInterceptor(),
		tracing.UnaryServerInterceptor(),
	))
	serv := grpcserver.NewServer(opts...)
	transportpb.RegisterUpdatesServer(serv, updatesServer)
	go func() {
		if err := serv.Serve(lis); err != nil {
			slog.ErrorContext(ctx, "gossip.listenUpdates: failed to serve", "err", err)
			return
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			slog.Info("gossip.listenUpdates: shutting down gossip receiver")
		}
	}()

	slog.InfoContext(ctx, "gossip.listenUpdates: listening gossip updates", "address", fmt.Sprintf("%s:%d", g.config.AdvertiseAddress, g.config.Port))

	return nil
}

func filterOutSelf(peers []types.Node, ourSelf types.Node) []types.Node {
	n := len(peers) - 1
	for ind := range peers {
		if peers[ind].ID() == ourSelf.ID() {
			peers[ind], peers[n] = peers[n], peers[ind]
			return peers[:n]
		}
	}
	return peers
}
