package noop

import (
	"context"

	"github.com/kestfor/in-memorydb/pkg/types"
)

type noopWAL struct{}

func NewNoopWAL() *noopWAL {
	return &noopWAL{}
}

func (w *noopWAL) Append(ctx context.Context, u types.Update) error {
	return nil
}

func (w *noopWAL) Get(nodeID string, seq uint64) (types.Update, error) {
	return types.Update{}, nil
}

func (w *noopWAL) Replay(ctx context.Context, nodeID string, fromSeq uint64, fn func(update types.Update) error) error {
	return nil
}

func (w *noopWAL) ReplayAll(ctx context.Context, fn func(update types.Update) error) error {
	return nil
}

func (w *noopWAL) Close() error {
	return nil
}
