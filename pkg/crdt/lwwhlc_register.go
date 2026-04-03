package crdt

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
)

type LWWHLCRegisterDelta struct {
	Value []byte        `json:"value"`
	TS    hlc.Timestamp `json:"ts"`
}

func (d *LWWHLCRegisterDelta) Merge(other Delta) error {
	od, ok := other.(*LWWHLCRegisterDelta)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T", d, other)
	}

	if d.TS.IsZero() || (!od.TS.IsZero() && d.TS.Before(od.TS)) {
		d.Value = od.Value
		d.TS = od.TS
	}

	return nil
}

func (d *LWWHLCRegisterDelta) CreateCRDT() (CRDT, error) {
	return &LWWHLCRegister{
		value: d.Value,
		ts:    d.TS,
	}, nil
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
	ts    hlc.Timestamp
	clock *hlc.Time
}

func NewLWWHLCRegister(id string) *LWWHLCRegister {
	clock := hlc.NewHLC(id)
	return &LWWHLCRegister{
		id:    id,
		clock: clock,
	}
}

// Value returns json.RawMessage - current register value
func (r *LWWHLCRegister) Value() any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value
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

	if r.ts.IsZero() || r.ts.Before(d.TS) {
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

	if r.ts.IsZero() || r.ts.Before(o.ts) {
		r.value = o.value
		r.ts = o.ts
	}

	return nil
}

// MarshalJSON сериализует регистр
func (r *LWWHLCRegister) MarshalJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// If value is valid JSON, use it directly as RawMessage.
	// Otherwise, encode the raw bytes as a JSON string so Marshal won't fail.
	var val json.RawMessage
	if len(r.value) > 0 && json.Valid(r.value) {
		val = r.value
	} else if len(r.value) > 0 {
		// Wrap non-JSON bytes as a JSON string
		quoted, _ := json.Marshal(string(r.value))
		val = quoted
	}

	data := struct {
		ID        string          `json:"id"`
		Value     json.RawMessage `json:"value"`
		Timestamp hlc.Timestamp   `json:"timestamp"`
	}{
		ID:        r.id,
		Value:     val,
		Timestamp: r.ts,
	}

	return json.Marshal(data)
}

// UnmarshalJSON десериализует регистр
func (r *LWWHLCRegister) UnmarshalJSON(data []byte) error {
	var tmp struct {
		ID        string          `json:"id"`
		Value     json.RawMessage `json:"value"`
		Timestamp hlc.Timestamp   `json:"timestamp"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.id = tmp.ID
	r.value = tmp.Value
	r.ts = tmp.Timestamp
	r.clock = hlc.NewHLC(r.id)

	if !r.ts.IsZero() {
		r.clock.SyncWithRemote(r.ts)
	}

	return nil
}

func (r *LWWHLCRegister) Type() CRDTType {
	return CRDTTypeLWWHLCRegister
}

func (r *LWWHLCRegister) Hash() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h := fnv.New64a()
	h.Write(r.value)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, r.ts.WallTime)
	h.Write(buf)
	binary.LittleEndian.PutUint64(buf, r.ts.Lamport)
	h.Write(buf)
	h.Write([]byte(r.ts.ID))
	return h.Sum64()
}
