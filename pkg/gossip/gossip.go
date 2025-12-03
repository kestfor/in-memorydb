package gossip

import (
	"context"
	"in-memorydb/pkg/structs"
	types "in-memorydb/pkg/types"
)

//go:generate mockgen -source=gossip.go -destination=mocks/gossip.mock.go Gossip

type VersionVectorResponse struct {
	NodeID      string
	VectorClock types.VectorClock
}

type Gossip interface {
	// Start starts an anti-entropy process and returns a channel for updates that will be sent to other peers
	Start(ctx context.Context) (chan<- []*types.Update, error)

	// Shutdown stops gossip
	Shutdown() error

	// Send sends data to random n=fanout nodes
	Send(ctx context.Context, data []*types.Update) error

	// AsyncSend similar to send, but non-blocking and returns an error channel to read from
	AsyncSend(ctx context.Context, data []*types.Update) <-chan error

	// GetVersionVector retrieves the version vector from the random node based on the current state of the storage system.
	// If a peer wasn't specified, then a random one will be picked
	GetVersionVector(ctx context.Context, peer types.Node) (*VersionVectorResponse, error)

	// Pull retrieves data from the network based on the provided version vector, ensuring synchronization of state.
	// If a peer wasn't specified, then a random one will be picked
	Pull(ctx context.Context, peer types.Node, versions map[string][]structs.Range) ([]*types.Update, error)
}
