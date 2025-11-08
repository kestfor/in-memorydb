package crdt

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

const LWWHLCRegisterName = "LWWHLCRegister"

type HLCTimestamp struct {
	WallTime int64  `json:"wall_time"` // unix nano
	Lamport  int64  `json:"lamport"`   // локальный счетчик
	ID       string `json:"id"`        // replica ID для tie-break
}

// Сравнение двух HLC: возвращает -1,0,1
func compareHLC(a, b HLCTimestamp) int {
	if a.WallTime < b.WallTime {
		return -1
	}
	if a.WallTime > b.WallTime {
		return 1
	}
	if a.Lamport < b.Lamport {
		return -1
	}
	if a.Lamport > b.Lamport {
		return 1
	}
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}

type LWWHLCRegisterDelta struct {
	Value json.RawMessage `json:"value"`
	TS    HLCTimestamp    `json:"ts"`
}

func (d *LWWHLCRegisterDelta) Merge(other Delta) error {
	od, ok := other.(*LWWHLCRegisterDelta)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T", d, other)
	}
	if compareHLC(d.TS, od.TS) < 0 {
		d.Value = append(json.RawMessage(nil), od.Value...)
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
		Type:  LWWHLCRegisterName,
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
	if aux.Type != "" && aux.Type != LWWHLCRegisterName {
		return fmt.Errorf("invalid delta type, expected %s, got %s", LWWHLCRegisterName, aux.Type)
	}
	return nil
}

func (d *LWWHLCRegisterDelta) Type() string {
	return LWWHLCRegisterName
}

type LWWHLCRegister struct {
	mu       sync.RWMutex
	id       string
	value    json.RawMessage
	localHLC HLCTimestamp
}

func NewLWWHLCRegister(id uuid.UUID) *LWWHLCRegister {
	now := time.Now().UnixNano()
	return &LWWHLCRegister{
		id: id.String(),
		localHLC: HLCTimestamp{
			WallTime: now,
			Lamport:  0,
			ID:       id.String(),
		},
	}
}

// Write создает новую дельту
func (r *LWWHLCRegister) Write(value json.RawMessage) Delta {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UnixNano()
	if now > r.localHLC.WallTime {
		r.localHLC.WallTime = now
		r.localHLC.Lamport = 0
	} else {
		r.localHLC.Lamport++
	}

	r.value = append(json.RawMessage(nil), value...)

	delta := &LWWHLCRegisterDelta{
		Value: append(json.RawMessage(nil), value...),
		TS:    r.localHLC,
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

	if compareHLC(r.localHLC, d.TS) < 0 {
		r.value = append(json.RawMessage(nil), d.Value...)
		r.localHLC = d.TS
	}

	return nil
}

// Read возвращает текущее значение
func (r *LWWHLCRegister) Read() json.RawMessage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append(json.RawMessage(nil), r.value...)
}

// Merge объединяет два регистра
func (r *LWWHLCRegister) Merge(other CRDT) error {
	o, ok := other.(*LWWHLCRegister)
	if !ok {
		return fmt.Errorf("cannot merge %T with %T", r, other)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if compareHLC(r.localHLC, o.localHLC) < 0 {
		r.value = append(json.RawMessage(nil), o.value...)
		r.localHLC = o.localHLC
	}

	// синхронизируем lamport counter
	if o.localHLC.Lamport > r.localHLC.Lamport {
		r.localHLC.Lamport = o.localHLC.Lamport
	}

	return nil
}

// MarshalJSON сериализует регистр
func (r *LWWHLCRegister) MarshalJSON() ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	data := struct {
		ID       string          `json:"id"`
		Value    json.RawMessage `json:"value"`
		LocalHLC HLCTimestamp    `json:"local_hlc"`
	}{
		ID:       r.id,
		Value:    r.value,
		LocalHLC: r.localHLC,
	}

	return json.Marshal(data)
}

// UnmarshalJSON десериализует регистр
func (r *LWWHLCRegister) UnmarshalJSON(data []byte) error {
	var tmp struct {
		ID       string          `json:"id"`
		Value    json.RawMessage `json:"value"`
		LocalHLC HLCTimestamp    `json:"local_hlc"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.id = tmp.ID
	r.value = tmp.Value
	r.localHLC = tmp.LocalHLC
	return nil
}

func (r *LWWHLCRegister) Type() string {
	return LWWHLCRegisterName
}
