package updates_buffer

import "in-memorydb/pkg/storage"

//go:generate mockgen -source=updates_buffer.go -destination=mocks/buffer.mock.go UpdatesBuffer

type UpdatesBuffer interface {
	Put(updates ...*storage.Update)
	Get(key string, nodeID string) (*storage.Update, bool)
	PeekN(n int) []*storage.Update
	Remove(key, nodeID string) bool
	RemoveN(n int) (removedN int)
	Len() int
}
