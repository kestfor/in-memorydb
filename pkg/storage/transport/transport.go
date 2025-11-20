package transport

import (
	"context"
	"in-memorydb/pkg/storage"
)

//go:generate mockgen -source=transport.go -destination=mocks/transport.mock.go Transport

type Transport interface {
	// Send sends a batch of updates to the specified remote address within the provided context. Returns an error on failure.
	Send(ctx context.Context, addr string, data []*storage.Update) error

	// Pull retrieves a batch of updates from the specified remote address for the given version within the provided context.
	Pull(ctx context.Context, addr string, version storage.Version) ([]*storage.Update, error)
}
