package crdt

import (
	"encoding/json"
	"fmt"
	"sync"
)

type LWWHLCRegisterDelta struct {
	Value json.RawMessage `json:"value"`
	TS    *Timestamp      `json:"ts"`
}

func (d *LWWHLCRegisterDelta) Merge(other Delta) error {
	od, ok := other.(*LWWHLCRegisterDelta)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T", d, other)
	}

	if d.TS.Before(od.TS) {
		d.Value = od.Value
		d.TS = od.TS
	}

	return nil
}

func (d *LWWHLCRegisterDelta) MarshalJSON() ([]byte, error) {
	type Alias LWWHLCRegisterDelta
	return json.Marshal(struct {
		Type string `json:"type"`
		*Alias
	}{
		Type:  CRDTTypeLWWHLCRegister.String(),
		Alias: (*Alias)(d),
	})
}

func (d *LWWHLCRegisterDelta) UnmarshalJSON(data []byte) error {
	type Alias LWWHLCRegisterDelta
	aux := struct {
		Type string `json:"type"`
		*Alias
	}{
		Alias: (*Alias)(d),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.Type != "" && aux.Type != CRDTTypeLWWHLCRegister.String() {
		return fmt.Errorf("invalid delta type, expected %s, got %s", CRDTTypeLWWHLCRegister, aux.Type)
	}
	return nil
}

func (d *LWWHLCRegisterDelta) Type() CRDTType {
	return CRDTTypeLWWHLCRegister
}

type LWWHLCRegister struct {
	mu    sync.RWMutex
	id    string
	value json.RawMessage
	ts    *Timestamp
	clock *Time
}

func NewLWWHLCRegister(id string) *LWWHLCRegister {
	clock := NewHLC(id)
	return &LWWHLCRegister{
		id:    id,
		ts:    clock.Now(),
		clock: clock,
	}
}

// Write создает новую дельту
func (r *LWWHLCRegister) Write(value json.RawMessage) Delta {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.ts = r.clock.Now()
	r.value = value

	delta := &LWWHLCRegisterDelta{
		Value: r.value,
		TS:    r.ts,
	}

	return delta
}

// ApplyDelta применяет дельту
func (r *LWWHLCRegister) ApplyDelta(delta Delta) error {
	d, ok := delta.(*LWWHLCRegisterDelta)
	if !ok {
		return fmt.Errorf("cannot apply delta type %T", delta)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.clock.SyncWithRemote(d.TS)

	if r.ts.Before(d.TS) {
		r.value = d.Value
		r.ts = d.TS
	}

	return nil
}

// Read возвращает текущее значение
func (r *LWWHLCRegister) Read() json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value
}

// Merge объединяет два регистра
func (r *LWWHLCRegister) Merge(other CRDT) error {
	o, ok := other.(*LWWHLCRegister)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T", r, other)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.clock.SyncWithRemote(o.ts)

	if r.ts.Before(o.ts) {
		r.value = o.value
		r.ts = o.ts
	}

	return nil
}

// MarshalJSON сериализует регистр
func (r *LWWHLCRegister) MarshalJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data := struct {
		ID        string          `json:"id"`
		Value     json.RawMessage `json:"value"`
		Timestamp *Timestamp      `json:"timestamp"`
	}{
		ID:        r.id,
		Value:     r.value,
		Timestamp: r.ts,
	}

	return json.Marshal(data)
}

// UnmarshalJSON десериализует регистр
func (r *LWWHLCRegister) UnmarshalJSON(data []byte) error {
	var tmp struct {
		ID        string          `json:"id"`
		Value     json.RawMessage `json:"value"`
		Timestamp *Timestamp      `json:"timestamp"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.id = tmp.ID
	r.value = tmp.Value
	r.ts = tmp.Timestamp
	r.clock = NewHLC(r.id)

	if r.ts != nil {
		r.clock.SyncWithRemote(r.ts)
	}

	return nil
}

func (r *LWWHLCRegister) Type() CRDTType {
	return CRDTTypeLWWHLCRegister
}
