package gossip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"time"

	"github.com/kestfor/in-memorydb/pkg/gossip"
	"github.com/kestfor/in-memorydb/pkg/gossip/gossip_buffer"
	"github.com/kestfor/in-memorydb/pkg/membership"
	"github.com/kestfor/in-memorydb/pkg/observability/spans"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	"github.com/kestfor/in-memorydb/pkg/storage/engine"
	buffer "github.com/kestfor/in-memorydb/pkg/storage/updates_buffer"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager"
	versionmanagerv2 "github.com/kestfor/in-memorydb/pkg/storage/version_manager/v2"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/transport"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"github.com/kestfor/in-memorydb/pkg/types"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	grpcserver "google.golang.org/grpc"
)

type Config struct {
	AdvertiseAddress    string        `yaml:"bind_address" env:"GOSSIP_BIND_ADDRESS" required:"true"`
	Port                uint16        `yaml:"port" env:"GOSSIP_PORT" default:"8081" required:"true"`
	Protocol            string        `yaml:"protocol" env:"GOSSIP_PROTOCOL" default:"SWIM"`
	AntiEntropyInterval time.Duration `yaml:"interval" env:"GOSSIP_ANT_ENTROPY_INTERVAL" default:"5s"`
	Fanout              int           `yaml:"fanout" env:"GOSSIP_FANOUT" default:"3"`
	Retries             int           `yaml:"retries" env:"GOSSIP_RETRIES" default:"3"`

	// BufferSize is a gBuffer size, containing updates from other nodes to resend
	// flow: peek n from main buffer + peek m from gbuffer -> send to other nodes
	// n = MaxBatchSize - n, where n - updates_buffer.PeekBatchSize
	// for example:
	//		setting updates_buffer.PeekBatchSize ~ MaxBatchSize / len(peers)
	//		makes approximately equal updates distribution per peer
	//
	//		specifying for some peers lower BufferSize and higher updates_buffer.PeekBatchSize
	//	 	increases their updates distribution
	BufferSize   int `yaml:"buffer_size" env:"GOSSIP_BUFFER_SIZE" default:"5000"`
	MaxBatchSize int `yaml:"max_batch_size" env:"GOSSIP_MAX_BATCH_SIZE" default:"10000"`

	// The WorkerPoolSize parameter determines how many parallel communication goroutines can exist.
	WorkerPoolSize int `yaml:"worker_pool_size" env:"GOSSIP_WORKER_POOL_SIZE" default:"5"`

	RequestTimeout time.Duration `yaml:"-" env:"GOSSIP_REQUEST_TIMEOUT" default:"60s"`
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
	config          *Config
	transportConfig *grpc.TransportConfig
	memberlist      membership.Membership
	transport       transport.Transport
	versionManager  version_manager.VersionManager
	buffer          buffer.UpdatesBuffer // local updates buffer
	engine          engine.Engine        // for key-based anti-entropy server

	// gbuffer contains only updates from other nodes, not for current
	gbuffer    *gossip_buffer.GossipBuffer // buffer used for epidemic data sending
	wal        wal.WAL
	serverOpts []grpcserver.ServerOption

	updatesChannel    chan []types.Update // channel for clients updates, periodically reads and send data from this channel to other peers
	shutdown          context.CancelFunc  // shutdown is a function to cancel the context, used to trigger graceful shutdown of ongoing processes.
	antiEntropyBucket uint32              // current bucket for partitioned anti-entropy rotation
	numBuckets        uint32              // total number of hash buckets for anti-entropy
}

func NewDefaultGossip(config *Config, transportConfig *grpc.TransportConfig, transport transport.Transport, list membership.Membership, manager version_manager.VersionManager, wal wal.WAL, buffer buffer.UpdatesBuffer, engine engine.Engine, serverOpts ...grpcserver.ServerOption) *DefaultGossip {
	return &DefaultGossip{
		config:          config,
		transportConfig: transportConfig,
		transport:       transport,
		memberlist:      list,
		versionManager:  manager,
		wal:             wal,
		buffer:          buffer,
		engine:          engine,
		serverOpts:      serverOpts,
		gbuffer:         gossip_buffer.NewGossipBuffer(config.BufferSize),
		updatesChannel:  make(chan []types.Update, 100),
		numBuckets:      versionmanagerv2.DefaultNumBuckets,
	}
}

// Start initializes and starts the gossip process, returning a channel for updates and an error, if any occurs.
// It also registers grpc server for update exchange between peers
func (g *DefaultGossip) Start(ctx context.Context) (chan<- []types.Update, error) {
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

func (g *DefaultGossip) Send(ctx context.Context, data []types.Update) error {
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

func (g *DefaultGossip) AsyncSend(ctx context.Context, data []types.Update) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- g.Send(ctx, data)
		close(ch)
	}()

	return ch
}

// TODO добавить возможность указать конкретного peer
func (g *DefaultGossip) Pull(ctx context.Context, peer types.Node, version map[string][]structs.Range) ([]types.Update, error) {
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
	sem := make(chan struct{}, g.config.WorkerPoolSize)
	for updates := range g.updatesChannel {
		sem <- struct{}{}
		go func(u []types.Update) {
			defer func() { <-sem }()
			ctx, span := tracing.StartSpan(ctx, spans.SpanGossipReadUpdates, trace.WithAttributes(attribute.Int("updates_count", len(u))))
			defer span.End()

			clusterSize := len(g.memberlist.Members())

			for index := range u {
				u[index].TTL = getTTLNumForAsync(clusterSize, g.config.Fanout, int(g.config.AntiEntropyInterval.Seconds()))
			}

			ttlUpds := g.gbuffer.PeekN(g.config.MaxBatchSize - len(u))
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
	ticker := time.NewTicker(g.config.AntiEntropyInterval)
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

// antiEntropyRound executes key-based anti-entropy: compare per-key digests for current bucket, pull stale keys, merge CRDT state.
// Rotates through buckets each round for partitioned coverage.
func (g *DefaultGossip) antiEntropyRound(ctx context.Context) {
	bucket := g.antiEntropyBucket
	g.antiEntropyBucket = (g.antiEntropyBucket + 1) % g.numBuckets

	ctx, span := tracing.StartSpan(ctx, spans.SpanGossipAntiEntropyRound, trace.WithAttributes(
		attribute.String("node_id", g.memberlist.LocalNode().ID()),
		attribute.Int("bucket", int(bucket)),
	))
	defer span.End()

	peer, err := g.getRandomPeer()
	if err != nil {
		slog.Debug("gossip.antiEntropyRound: No need for anti-entropy, node in standalone mode", "err", err)
		return
	}
	span.SetAttributes(attribute.String("peer", peer.ID()))

	withTimeOut, cancel := context.WithTimeout(ctx, g.config.RequestTimeout)
	defer cancel()

	// Step 1: Get remote key digests for this bucket
	remoteDigests, err := g.transport.GetKeyDigests(withTimeOut, peer.GossipAddr().String(), bucket)
	if err != nil {
		slog.Error("gossip.antiEntropyRound: failed to get key digests", "err", err, "peer", peer.ID(), "bucket", bucket)
		return
	}

	// Step 2: Compare with local digests for this bucket
	localDigests := g.versionManager.KeyDigests(bucket)
	var staleKeys []string

	for key, remoteHash := range remoteDigests {
		localHash, exists := localDigests[key]
		if !exists || localHash != remoteHash {
			staleKeys = append(staleKeys, key)
		}
	}

	if len(staleKeys) == 0 {
		slog.DebugContext(ctx, "gossip.antiEntropyRound: No stale keys found", "peer_id", peer.ID(), "bucket", bucket)
		return
	}

	// Step 3: Pull key states for stale keys
	withTimeOut, cancel = context.WithTimeout(ctx, g.config.RequestTimeout)
	defer cancel()

	keyStates, err := g.transport.PullKeyStates(withTimeOut, peer.GossipAddr().String(), staleKeys)
	if err != nil {
		slog.Error("gossip.antiEntropyRound: failed to pull key states", "err", err, "peer", peer.ID())
		return
	}

	slog.InfoContext(ctx, "gossip.antiEntropyRound: Pulled key states",
		"stale_keys", len(staleKeys), "received_states", len(keyStates), "from_peer", peer.ID(), "bucket", bucket)

	// Step 4: Merge each key state
	for _, ks := range keyStates {
		if err := g.versionManager.MergeKeyState(ctx, ks); err != nil {
			slog.Error("gossip.antiEntropyRound: failed to merge key state",
				"error", err, "key", ks.Key, "peer", peer.ID())
		}
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

	updatesServer := grpc.NewUpdatesServer(g.buffer, g.gbuffer, g.wal, g.versionManager, g.engine, g.transportConfig.MaxBatchSize)
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
