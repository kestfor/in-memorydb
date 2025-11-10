package storage

import "in-memorydb/pkg/crdt"

type Store struct {
	engine *Engine
	vm     *VersionManager
}

func NewStore(nodeID string, engine *Engine) *Store {
	return &Store{
		engine: engine,
		vm:     NewVersionManager(nodeID, engine),
	}
	// Todo запуск vm в фоне для обновления
}

// нужны адаптеры чтобы отдавать не entry а адаптеры, который внутри себя хендлят логику работы с crdt и используют version manager для синхронизации

func (store *Store) Get(key string) (*CRDTEntry, bool) {
	return store.engine.Get(key)
}

func (store *Store) Put(key string, obj crdt.CRDT) {
	store.engine.Put(key, obj)
}

func (store *Store) Delete(key string) {
	store.engine.Delete(key)
}
