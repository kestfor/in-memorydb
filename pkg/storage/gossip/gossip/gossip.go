package gossip

import (
	"context"
	"fmt"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/storage"
	"in-memorydb/pkg/storage/gossip"
	"in-memorydb/pkg/storage/transport"
	"in-memorydb/pkg/structs"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/hashicorp/memberlist"
)

type DefaultGossip struct {
	config         *config.GossipConfig
	memberlist     *memberlist.Memberlist
	transport      transport.Transport
	versionManager *storage.VersionManager

	updatesChannel chan []*storage.Update
}

func NewDefaultGossip(config *config.GossipConfig, transport transport.Transport, list *memberlist.Memberlist, manager *storage.VersionManager) *DefaultGossip {
	return &DefaultGossip{
		config:         config,
		transport:      transport,
		memberlist:     list,
		versionManager: manager,
		updatesChannel: make(chan []*storage.Update, 10), // TODO настроить размер
	}
}

func (g *DefaultGossip) Start(ctx context.Context) chan<- []*storage.Update {
	go g.runAntiEntropy(ctx)
	go g.readUpdates(ctx)
	return g.updatesChannel
}

func (g *DefaultGossip) Send(ctx context.Context, data []*storage.Update) error {
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

func (g *DefaultGossip) AsyncSend(ctx context.Context, data []*storage.Update) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- g.Send(ctx, data)
		close(ch)
	}()

	return ch
}

func (g *DefaultGossip) Pull(ctx context.Context, version storage.Version) ([]*storage.Update, error) {
	peer, err := g.getRandomPeer()
	if err != nil {
		return nil, err
	}
	return g.transport.Pull(ctx, peer.Addr.String(), version)
}

func (g *DefaultGossip) GetVersionVector(ctx context.Context) (*gossip.VersionVectorResponse, error) {
	peer, err := g.getRandomPeer()
	if err != nil {
		return nil, err
	}
	version, err := g.transport.GetVersion(ctx, peer.Addr.String())
	if err != nil {
		return nil, err
	}
	return &gossip.VersionVectorResponse{
		NodeID:        peer.Name,
		VersionVector: version,
	}, nil

}

func (g *DefaultGossip) readUpdates(ctx context.Context) {
	sem := make(chan struct{}, 5) // TODO где MaxConcurrentSends в конфиге
	for updates := range g.updatesChannel {
		sem <- struct{}{}
		go func(u []*storage.Update) {
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
		return nil, fmt.Errorf("no peers found")
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
			return
		case <-ticker.C:
			g.antiEntropyRound(ctx)
		}
	}
}

func (g *DefaultGossip) antiEntropyRound(ctx context.Context) {
	withTimeOut, cancel := context.WithTimeout(ctx, time.Second) // TODO выбрать timeout через конфиг
	received, err := g.GetVersionVector(withTimeOut)
	cancel()
	if err != nil {
		slog.Error("Error getting version vector", err)
		return
	}

	currVersion := g.versionManager.GetVersionVector()
	diff := storage.VersionDifference(currVersion, received.VersionVector)

	withTimeOut, cancel = context.WithTimeout(ctx, time.Second)
	updates, err := g.Pull(withTimeOut, diff)
	cancel()
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
