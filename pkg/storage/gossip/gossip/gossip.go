package gossip

import (
	"context"
	"errors"
	"fmt"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/storage/gossip"
	"in-memorydb/pkg/storage/transport"
	"in-memorydb/pkg/storage/types"
	"in-memorydb/pkg/storage/version_manager"
	"in-memorydb/pkg/structs"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/hashicorp/memberlist"
)

var ErrNoPeers = errors.New("no peers available")

type DefaultGossip struct {
	config         *config.GossipConfig
	memberlist     *memberlist.Memberlist
	transport      transport.Transport
	versionManager *version_manager.VersionManager

	updatesChannel chan []*types.Update
	shutdown       context.CancelFunc
}

func NewDefaultGossip(config *config.GossipConfig, transport transport.Transport, list *memberlist.Memberlist, manager *version_manager.VersionManager) *DefaultGossip {
	return &DefaultGossip{
		config:         config,
		transport:      transport,
		memberlist:     list,
		versionManager: manager,
		updatesChannel: make(chan []*types.Update, 10), // TODO настроить размер
	}
}

func (g *DefaultGossip) Start(ctx context.Context) chan<- []*types.Update {
	ctx, cancel := context.WithCancel(ctx)
	g.shutdown = cancel
	go g.runAntiEntropy(ctx)
	go g.readUpdates(ctx)
	return g.updatesChannel
}

func (g *DefaultGossip) Shutdown() error {
	g.shutdown()
	time.Sleep(time.Second)
	return nil
}

func (g *DefaultGossip) Send(ctx context.Context, data []*types.Update) error {
	peers := filterOutSelf(g.memberlist.Members(), g.memberlist.LocalNode())
	picked := structs.NewSet[int]()
	fanout := min(len(peers), g.config.Fanout)
	successNum := 0

	for successNum < fanout {
		if len(picked) == len(peers) {
			return fmt.Errorf("too few active peers")
		}

		ind := rand.IntN(len(peers))
		if picked.Contains(ind) {
			continue
		}
		peer := peers[ind]

		for _ = range g.config.Retries {
			err := g.transport.Send(ctx, peer.Addr.String(), data)
			if err != nil {
				slog.Warn("error while sending data to peer", "peer", peer.Addr, "err", err)
				continue
			}
			successNum++
			break
		}

		picked.Add(ind)
	}
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
func (g *DefaultGossip) Pull(ctx context.Context, peer *memberlist.Node, version map[string][]structs.Range) ([]*types.Update, error) {
	if peer == nil {
		var err error
		peer, err = g.getRandomPeer()
		if err != nil {
			return nil, err
		}
	}
	return g.transport.Pull(ctx, peer.Addr.String(), version)
}

func (g *DefaultGossip) GetVersionVector(ctx context.Context, peer *memberlist.Node) (*gossip.VersionVectorResponse, error) {
	if peer == nil {
		var err error
		peer, err = g.getRandomPeer()
		if err != nil {
			return nil, err
		}
	}
	version, err := g.transport.GetVersion(ctx, peer.Addr.String())
	if err != nil {
		return nil, err
	}
	return &gossip.VersionVectorResponse{
		NodeID:      peer.Name,
		VectorClock: version,
	}, nil

}

func (g *DefaultGossip) readUpdates(ctx context.Context) {
	sem := make(chan struct{}, 5) // TODO где MaxConcurrentSends в конфиге
	for updates := range g.updatesChannel {
		sem <- struct{}{}
		go func(u []*types.Update) {
			defer func() { <-sem }()
			if err := <-g.AsyncSend(ctx, u); err != nil {
				slog.Warn("async send failed", "err", err)
			}
		}(updates)
	}
}

func (g *DefaultGossip) getRandomPeer() (*memberlist.Node, error) {
	peers := filterOutSelf(g.memberlist.Members(), g.memberlist.LocalNode())
	if len(peers) == 0 {
		return nil, ErrNoPeers
	}
	ind := rand.IntN(len(peers))
	return peers[ind], nil
}

func (g *DefaultGossip) runAntiEntropy(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(g.config.AntiEntropyIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "shutting down anti-entropy")
			return
		case <-ticker.C:
			g.antiEntropyRound(ctx)
		}
	}
}

func (g *DefaultGossip) antiEntropyRound(ctx context.Context) {
	withTimeOut, cancel := context.WithTimeout(ctx, time.Second*60) // TODO выбрать timeout через конфиг
	defer cancel()

	peer, err := g.getRandomPeer()
	if err != nil {
		slog.Debug("No need for anti-entropy, node in standalone mode", "err", err)
		return
	}

	received, err := g.GetVersionVector(withTimeOut, peer)

	if err != nil {
		slog.Error("anti-entropy failed", "err", err)
		return
	}

	diff := g.versionManager.VersionDiff(received.VectorClock)

	if len(diff) == 0 {
		slog.DebugContext(ctx, "No difference with peer found")
		return
	}

	withTimeOut, cancel = context.WithTimeout(withTimeOut, time.Second*60)
	defer cancel()
	updates, err := g.Pull(withTimeOut, peer, diff)
	if err != nil {
		slog.Error("Error pulling updates", err)
		return
	}

	if len(updates) > 0 {
		go func() {
			g.versionManager.Update(updates...)
		}()
	}
}

func filterOutSelf(peers []*memberlist.Node, ourSelf *memberlist.Node) []*memberlist.Node {
	n := len(peers) - 1
	for ind := range peers {
		if peers[ind].Name == ourSelf.Name {
			peers[ind], peers[n] = peers[n], peers[ind]
		}
	}
	return peers[:n]
}
