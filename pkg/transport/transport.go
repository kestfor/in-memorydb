package transport

import (
	"context"
	"in-memorydb/pkg/structs"
	types "in-memorydb/pkg/types"
)

//go:generate mockgen -source=transport.go -destination=mocks/transport.mock.go Transport

type Transport interface {
	// Send sends a batch of updates to the specified remote address within the provided context. Returns an error on failure.
	Send(ctx context.Context, addr string, data []*types.Update) error

	// Pull retrieves a batch of updates from the specified remote address for the given versions within the provided context.
	Pull(ctx context.Context, addr string, versions map[string][]structs.Range) ([]*types.Update, error)

	// GetVersion retrieves version vector from specified addr
	GetVersion(ctx context.Context, addr string) (types.VectorClock, error)
}
