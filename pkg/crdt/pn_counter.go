package crdt

import (
	"encoding/json"
	"fmt"
	"in-memorydb/pkg/structs"
	"sync"
)

// PNCounter — распределённый счётчик с поддержкой инкремента/декремента, thread-safe но мб это оверхед
type PNCounter struct {
	mu sync.RWMutex
	id string
	P  map[string]int64 // положительные инкременты
	N  map[string]int64 // отрицательные инкременты
}

// PNCounterDelta — дельта для PNCounter, не thread-safe
type PNCounterDelta struct {
	P map[string]int64
	N map[string]int64
}

var _ CRDT = (*PNCounter)(nil)

func (d *PNCounterDelta) Merge(other Delta) error {
	otherDelta, ok := other.(*PNCounterDelta)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T: %w", d, other, ErrDeltaTypeMismatch)
	}

	for node, val := range otherDelta.P {
		d.P[node] += val
	}
	for node, val := range otherDelta.N {
		d.N[node] += val
	}
	return nil
}

func (P PNCounterDelta) MarshalJSON() ([]byte, error) {
	type Alias PNCounterDelta
	return json.Marshal(struct {
		Type string `json:"type"`
		*Alias
	}{
		Type:  CRDTTypePNCounter.String(),
		Alias: (*Alias)(&P),
	})
}

func (P *PNCounterDelta) UnmarshalJSON(data []byte) error {
	type Alias PNCounterDelta
	var aux struct {
		Type string `json:"type"`
		*Alias
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Type != "" && aux.Type != CRDTTypePNCounter.String() {
		return ErrInvalidDeltaType
	}

	*P = PNCounterDelta(*aux.Alias)
	return nil
}

func (P PNCounterDelta) Type() CRDTType {
	return CRDTTypePNCounter
}

func (c *PNCounter) Type() CRDTType {
	return CRDTTypePNCounter
}

func NewPNCounter(id string) *PNCounter {
	return &PNCounter{
		id: id,
		P:  make(map[string]int64),
		N:  make(map[string]int64),
	}
}

func (c *PNCounter) UnmarshalJSON(snapshot []byte) error {
	var data struct {
		ID string
		P  map[string]int64
		N  map[string]int64
	}

	if err := json.Unmarshal(snapshot, &data); err != nil {
		return err
	}

	c.id = data.ID
	c.P = data.P
	c.N = data.N

	return nil
}

func (c *PNCounter) Increment(delta int64) Delta {
	c.mu.Lock()
	c.P[c.id] += delta
	c.mu.Unlock()
	return &PNCounterDelta{
		P: map[string]int64{c.id: delta},
	}
}

func (c *PNCounter) Decrement(delta int64) Delta {
	c.mu.Lock()
	c.N[c.id] += delta
	c.mu.Unlock()
	return &PNCounterDelta{
		N: map[string]int64{c.id: delta},
	}
}

func (c *PNCounter) Value() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sumP, sumN int64
	for _, v := range c.P {
		sumP += v
	}
	for _, v := range c.N {
		sumN += v
	}
	return sumP - sumN
}

// Merge объединяет два счётчика
func (c *PNCounter) Merge(other CRDT) error {
	o, ok := other.(*PNCounter)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T: %w", c, other, ErrCRDTTypeMismatch)
	}
	c.mergePNCounter(o)
	return nil
}

func (c *PNCounter) ApplyDelta(delta Delta) error {
	d, ok := delta.(*PNCounterDelta)
	if !ok {
		return fmt.Errorf("cannot apply delta with type %T to %T: %w", delta, c, ErrDeltaTypeMismatch)
	}

	c.mu.Lock()
	for node, val := range d.P {
		c.P[node] = max(c.P[node], val)
	}

	for node, val := range d.N {
		c.N[node] = max(c.N[node], val)
	}
	c.mu.Unlock()

	return nil
}

func (c *PNCounter) MergeSnapshot(snapshot []byte) error {
	var other PNCounter
	if err := json.Unmarshal(snapshot, &other); err != nil {
		return err
	}

	return c.Merge(&other)
}

func (c *PNCounter) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data := struct {
		ID string
		P  map[string]int64
		N  map[string]int64
	}{
		ID: c.id,
		P:  c.P,
		N:  c.N,
	}

	return json.Marshal(data)
}

// IDs возвращает список всех известных узлов
func (c *PNCounter) IDs() structs.Set[string] {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := structs.NewSet[string]()
	for id := range c.P {
		ids.Add(id)
	}
	for id := range c.N {
		ids.Add(id)
	}

	return ids
}

func (c *PNCounter) mergePNCounter(other *PNCounter) {
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
