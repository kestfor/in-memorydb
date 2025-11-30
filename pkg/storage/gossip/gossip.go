package gossip

import (
	"context"
	"in-memorydb/pkg/storage/types"
	"in-memorydb/pkg/structs"

	"github.com/hashicorp/memberlist"
)

//go:generate mockgen -source=gossip.go -destination=mocks/gossip.mock.go Gossip

type VersionVectorResponse struct {
	NodeID      string
	VectorClock types.VectorClock
}

type Gossip interface {
	// Start starts an anti-entropy process and returns a channel for updates that will be sent to other peers
	Start(ctx context.Context) chan<- []*types.Update

	// Shutdown stops gossip
	Shutdown() error

	// Send sends data to random n=fanout nodes
	Send(ctx context.Context, data []*types.Update) error

	// AsyncSend similar to send, but non-blocking and returns an error channel to read from
	// if peer not specified random picked
	AsyncSend(ctx context.Context, data []*types.Update) <-chan error

	// GetVersionVector retrieves the version vector from the random node based on the current state of the storage system.
	// if peer not specified random picked
	GetVersionVector(ctx context.Context, peer *memberlist.Node) (*VersionVectorResponse, error)

	// Pull retrieves data from the network based on the provided version vector, ensuring synchronization of state.
	// if peer not specified random picked
	Pull(ctx context.Context, peer *memberlist.Node, versions map[string][]structs.Range) ([]*types.Update, error)
}
