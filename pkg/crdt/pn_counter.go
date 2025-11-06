package crdt

import (
	"encoding/json"
	"in-memorydb/pkg/structs"
	"sync"

	"golang.org/x/exp/constraints"
)

const PNCounterName = "PNCounter"

// PNCounter — распределённый счётчик с поддержкой инкремента/декремента
type PNCounter[T comparable, V constraints.Integer] struct {
	mu sync.RWMutex
	id T       // ID узла
	P  map[T]V // положительные инкременты
	N  map[T]V // отрицательные инкременты
}

func NewPNCounter[T comparable, V constraints.Integer](id T) *PNCounter[T, V] {
	return &PNCounter[T, V]{
		id: id,
		P:  make(map[T]V),
		N:  make(map[T]V),
	}
}

func NewPNCounterFromSnapshot[T comparable, V constraints.Integer](snapshot []byte) (*PNCounter[T, V], error) {
	var data struct {
		ID T
		P  map[T]V
		N  map[T]V
	}

	if err := json.Unmarshal(snapshot, &data); err != nil {
		return nil, err
	}

	c := &PNCounter[T, V]{
		id: data.ID,
		P:  data.P,
		N:  data.N,
	}

	return c, nil
}

func (c *PNCounter[T, V]) Increment(delta V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.P[c.id] += delta
}

func (c *PNCounter[T, V]) Decrement(delta V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.N[c.id] += delta
}

func (c *PNCounter[T, V]) Value() V {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sumP, sumN V
	for _, v := range c.P {
		sumP += v
	}
	for _, v := range c.N {
		sumN += v
	}
	return sumP - sumN
}

// Merge объединяет два счётчика
func (c *PNCounter[T, V]) Merge(other *PNCounter[T, V]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for node, val := range other.P {
		if val > c.P[node] {
			c.P[node] = val
		}
	}
	for node, val := range other.N {
		if val > c.N[node] {
			c.N[node] = val
		}
	}
}

func (c *PNCounter[T, V]) MergeSnapshot(snapshot []byte) error {
	other, err := NewPNCounterFromSnapshot[T, V](snapshot)
	if err != nil {
		return err
	}
	c.Merge(other)
	return nil
}

// Snapshot возвращает снимок текущего состояния (например, для репликации или хранения)
func (c *PNCounter[T, V]) Snapshot() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data := struct {
		ID T
		P  map[T]V
		N  map[T]V
	}{
		ID: c.id,
		P:  c.P,
		N:  c.N,
	}

	return json.Marshal(data)
}

// IDs возвращает список всех известных узлов
func (c *PNCounter[T, V]) IDs() structs.Set[T] {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := structs.NewSet[T]()
	for id := range c.P {
		ids.Add(id)
	}
	for id := range c.N {
		ids.Add(id)
	}

	return ids
}
