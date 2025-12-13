package updates_buffer

import (
	"github/kestfor/in-memorydb/pkg/structs"
	"github/kestfor/in-memorydb/pkg/types"
)

//go:generate mockgen -source=updates_buffer.go -destination=mocks/buffer.mock.go UpdatesBuffer

type UpdatesBuffer interface {
	Put(updates ...*types.Update)
	Get(key string, nodeID string) ([]*types.Update, bool)
	PeekN(n int) []*types.Update
	Remove(key, nodeID string) bool
	RemoveN(n int) (removedN int)
	GetCovering(nodeID string, r structs.Range) []*types.Update
	Len() int
}
