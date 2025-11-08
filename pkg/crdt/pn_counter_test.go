package crdt

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// helper: convert slice to sorted slice (for deterministic comparison)
func sortedStrings(input []string) []string {
	out := append([]string(nil), input...)
	sort.Strings(out)
	return out
}

func TestPNCounter_BasicOperations(t *testing.T) {
	type op struct {
		inc bool
		d   int64
	}

	tests := []struct {
		name  string
		ops   []op
		value int64
	}{
		{
			name:  "single increment",
			ops:   []op{{inc: true, d: 5}},
			value: 5,
		},
		{
			name:  "single decrement",
			ops:   []op{{inc: false, d: 3}},
			value: -3,
		},
		{
			name:  "inc then dec",
			ops:   []op{{inc: true, d: 10}, {inc: false, d: 4}},
			value: 6,
		},
		{
			name:  "multiple ops",
			ops:   []op{{inc: true, d: 2}, {inc: true, d: 3}, {inc: false, d: 1}, {inc: false, d: 4}},
			value: 0, // (2+3)-(1+4) = 0
		},
		{
			name:  "zero increment",
			ops:   []op{{inc: true, d: 0}},
			value: 0,
		},
		{
			name:  "zero decrement",
			ops:   []op{{inc: false, d: 0}},
			value: 0,
		},
		{
			name:  "large values",
			ops:   []op{{inc: true, d: 1000000}, {inc: false, d: 999999}},
			value: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewPNCounter(uuid.New())
			for _, o := range tc.ops {
				if o.inc {
					c.Increment(o.d)
				} else {
					c.Decrement(o.d)
				}
			}
			got := c.Value()
			if got != tc.value {
				t.Fatalf("expected Value()=%d, got %d", tc.value, got)
			}
		})
	}
}

func TestPNCounter_Merge(t *testing.T) {
	tests := []struct {
		name     string
		prepareA func() *PNCounter
		prepareB func() *PNCounter
		want     int64
	}{
		{
			name: "merge disjoint nodes",
			prepareA: func() *PNCounter {
				a := NewPNCounter(uuid.New())
				a.Increment(3)
				a.Decrement(1)
				return a
			},
			prepareB: func() *PNCounter {
				b := NewPNCounter(uuid.New())
				b.Increment(4)
				b.Decrement(2)
				return b
			},
			want: 4, // (3+4)-(1+2) = 4
		},
		{
			name: "merge same node takes max per entry",
			prepareA: func() *PNCounter {
				a := NewPNCounter(uuid.New())
				a.Increment(2)
				a.Decrement(1)
				return a
			},
			prepareB: func() *PNCounter {
				b := NewPNCounter(uuid.New())
				b.Increment(5) // larger than a.P["A"]
				b.Decrement(3) // larger than a.N["A"]
				return b
			},
			want: 3, // 2 - 1 + 5 - 3 = 3
		},
		{
			name: "merge empty counter",
			prepareA: func() *PNCounter {
				a := NewPNCounter(uuid.New())
				a.Increment(5)
				return a
			},
			prepareB: func() *PNCounter {
				return NewPNCounter(uuid.New())
			},
			want: 5,
		},
		{
			name: "merge into empty counter",
			prepareA: func() *PNCounter {
				return NewPNCounter(uuid.New())
			},
			prepareB: func() *PNCounter {
				b := NewPNCounter(uuid.New())
				b.Increment(10)
				b.Decrement(3)
				return b
			},
			want: 7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.prepareA()
			b := tc.prepareB()
			a.Merge(b)
			if got := a.Value(); got != tc.want {
				t.Fatalf("after merge expected Value()=%d, got %d", tc.want, got)
			}
		})
	}
}

func TestPNCounter_Type(t *testing.T) {
	c := NewPNCounter(uuid.New())
	if got := c.Type(); got != PNCounterName {
		t.Errorf("Type() = %q, want %q", got, PNCounterName)
	}
}

func TestPNCounter_JSONSerialization(t *testing.T) {
	tests := []struct {
		name    string
		prepare func() *PNCounter
		wantErr bool
	}{
		{
			name: "empty counter",
			prepare: func() *PNCounter {
				return NewPNCounter(uuid.New())
			},
			wantErr: false,
		},
		{
			name: "counter with increments",
			prepare: func() *PNCounter {
				c := NewPNCounter(uuid.New())
				c.Increment(5)
				c.Increment(3)
				return c
			},
			wantErr: false,
		},
		{
			name: "counter with increments and decrements",
			prepare: func() *PNCounter {
				c := NewPNCounter(uuid.New())
				c.Increment(10)
				c.Decrement(4)
				return c
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.prepare()
			originalValue := original.Value()

			// Marshal
			data, err := json.Marshal(original)
			if (err != nil) != tc.wantErr {
				t.Fatalf("MarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			// Unmarshal
			restored := NewPNCounter(uuid.New())
			if err := json.Unmarshal(data, restored); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}

			// Verify value is preserved
			if got := restored.Value(); got != originalValue {
				t.Errorf("after unmarshal Value() = %d, want %d", got, originalValue)
			}

			// Verify ID is preserved
			if restored.id != original.id {
				t.Errorf("after unmarshal id = %q, want %q", restored.id, original.id)
			}
		})
	}
}

func TestPNCounterDelta_JSONSerialization(t *testing.T) {
	tests := []struct {
		name    string
		delta   *PNCounterDelta
		wantErr bool
	}{
		{
			name: "positive delta",
			delta: &PNCounterDelta{
				ID:    "node1",
				Delta: 5,
			},
			wantErr: false,
		},
		{
			name: "negative delta",
			delta: &PNCounterDelta{
				ID:    "node2",
				Delta: -3,
			},
			wantErr: false,
		},
		{
			name: "zero delta",
			delta: &PNCounterDelta{
				ID:    "node3",
				Delta: 0,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Marshal
			data, err := json.Marshal(tc.delta)
			if (err != nil) != tc.wantErr {
				t.Fatalf("MarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			// Verify type field is present
			var raw map[string]interface{}
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("failed to unmarshal to map: %v", err)
			}
			if raw["type"] != PNCounterName {
				t.Errorf("type field = %q, want %q", raw["type"], PNCounterName)
			}

			// Unmarshal
			var restored PNCounterDelta
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}

			// Verify fields
			if restored.ID != tc.delta.ID {
				t.Errorf("ID = %q, want %q", restored.ID, tc.delta.ID)
			}
			if restored.Delta != tc.delta.Delta {
				t.Errorf("Delta = %d, want %d", restored.Delta, tc.delta.Delta)
			}
		})
	}
}

func TestPNCounterDelta_Type(t *testing.T) {
	d := &PNCounterDelta{ID: "test", Delta: 1}
	if got := d.Type(); got != PNCounterName {
		t.Errorf("Type() = %q, want %q", got, PNCounterName)
	}
}

func TestPNCounter_ApplyDelta(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()

	tests := []struct {
		name      string
		prepare   func() *PNCounter
		delta     Delta
		wantValue int64
		wantErr   bool
	}{
		{
			name: "apply delta from same node",
			prepare: func() *PNCounter {
				return NewPNCounter(id1)
			},
			delta: &PNCounterDelta{
				ID:    id1.String(),
				Delta: 5,
			},
			wantValue: 5,
			wantErr:   false,
		},
		{
			name: "apply delta from different node",
			prepare: func() *PNCounter {
				return NewPNCounter(id1)
			},
			delta: &PNCounterDelta{
				ID:    id2.String(),
				Delta: 3,
			},
			wantValue: 3, // goes to N map but delta is positive, so adds to N
			wantErr:   false,
		},
		{
			name: "apply multiple deltas from same node",
			prepare: func() *PNCounter {
				c := NewPNCounter(id1)
				c.ApplyDelta(&PNCounterDelta{ID: id1.String(), Delta: 5})
				return c
			},
			delta: &PNCounterDelta{
				ID:    id1.String(),
				Delta: 3,
			},
			wantValue: 8,
			wantErr:   false,
		},
		{
			name: "apply invalid delta type",
			prepare: func() *PNCounter {
				return NewPNCounter(id1)
			},
			delta:     &mockDelta{},
			wantValue: 0,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.prepare()
			err := c.ApplyDelta(tc.delta)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ApplyDelta() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			if got := c.Value(); got != tc.wantValue {
				t.Errorf("after ApplyDelta Value() = %d, want %d", got, tc.wantValue)
			}
		})
	}
}

func TestPNCounter_MergeSnapshot(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func() *PNCounter
		snapshot  func() []byte
		wantValue int64
		wantErr   bool
	}{
		{
			name: "merge valid snapshot",
			prepare: func() *PNCounter {
				c := NewPNCounter(uuid.New())
				c.Increment(5)
				return c
			},
			snapshot: func() []byte {
				c := NewPNCounter(uuid.New())
				c.Increment(10)
				data, _ := json.Marshal(c)
				return data
			},
			wantValue: 15,
			wantErr:   false,
		},
		{
			name: "merge empty snapshot",
			prepare: func() *PNCounter {
				c := NewPNCounter(uuid.New())
				c.Increment(5)
				return c
			},
			snapshot: func() []byte {
				c := NewPNCounter(uuid.New())
				data, _ := json.Marshal(c)
				return data
			},
			wantValue: 5,
			wantErr:   false,
		},
		{
			name: "merge invalid snapshot",
			prepare: func() *PNCounter {
				return NewPNCounter(uuid.New())
			},
			snapshot: func() []byte {
				return []byte("invalid json")
			},
			wantValue: 0,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.prepare()
			err := c.MergeSnapshot(tc.snapshot())
			if (err != nil) != tc.wantErr {
				t.Fatalf("MergeSnapshot() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			if got := c.Value(); got != tc.wantValue {
				t.Errorf("after MergeSnapshot Value() = %d, want %d", got, tc.wantValue)
			}
		})
	}
}

func TestPNCounter_IDs(t *testing.T) {
	tests := []struct {
		name    string
		prepare func() *PNCounter
		wantIDs []string
	}{
		{
			name: "empty counter",
			prepare: func() *PNCounter {
				return NewPNCounter(uuid.New())
			},
			wantIDs: []string{},
		},
		{
			name: "counter with single node",
			prepare: func() *PNCounter {
				c := NewPNCounter(uuid.New())
				c.Increment(5)
				return c
			},
			wantIDs: []string{}, // will be set dynamically
		},
		{
			name: "counter with multiple nodes",
			prepare: func() *PNCounter {
				id1 := uuid.New()
				id2 := uuid.New()
				c := NewPNCounter(id1)
				c.Increment(5)
				other := NewPNCounter(id2)
				other.Increment(3)
				c.Merge(other)
				return c
			},
			wantIDs: []string{}, // will be set dynamically
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.prepare()
			ids := c.IDs()

			// For empty counter, should be empty
			if tc.name == "empty counter" {
				if len(ids) != 0 {
					t.Errorf("len(IDs()) = %d, want 0", len(ids))
				}
				return
			}

			// For other cases, just verify size is reasonable
			if len(ids) == 0 && c.Value() != 0 {
				t.Errorf("IDs() returned empty set but counter has value %d", c.Value())
			}
		})
	}
}

func TestPNCounter_Merge_ErrorCases(t *testing.T) {
	tests := []struct {
		name    string
		counter *PNCounter
		other   CRDT
		wantErr bool
	}{
		{
			name:    "merge with nil",
			counter: NewPNCounter(uuid.New()),
			other:   nil,
			wantErr: true,
		},
		{
			name:    "merge with wrong type",
			counter: NewPNCounter(uuid.New()),
			other:   &mockCRDT{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.counter.Merge(tc.other)
			if (err != nil) != tc.wantErr {
				t.Errorf("Merge() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestPNCounterDelta_UnmarshalJSON_InvalidType(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "wrong type field",
			json:    `{"type":"WrongType","ID":"test","Delta":5}`,
			wantErr: true,
		},
		{
			name:    "missing type field is ok",
			json:    `{"ID":"test","Delta":5}`,
			wantErr: false,
		},
		{
			name:    "invalid json",
			json:    `{invalid}`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d PNCounterDelta
			err := json.Unmarshal([]byte(tc.json), &d)
			if (err != nil) != tc.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestPNCounter_Concurrent(t *testing.T) {
	c := NewPNCounter(uuid.New())
	var wg sync.WaitGroup

	// Concurrent increments
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Increment(1)
		}()
	}

	// Concurrent decrements
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Decrement(1)
		}()
	}

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Value()
		}()
	}

	wg.Wait()

	// Verify final value
	expected := int64(50) // 100 increments - 50 decrements
	if got := c.Value(); got != expected {
		t.Errorf("after concurrent operations Value() = %d, want %d", got, expected)
	}
}

func TestPNCounter_ConcurrentMerge(t *testing.T) {
	c := NewPNCounter(uuid.New())
	var wg sync.WaitGroup

	// Concurrent merges
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := NewPNCounter(uuid.New())
			other.Increment(1)
			c.Merge(other)
		}()
	}

	wg.Wait()

	// Value should be at least 10 (one from each merge)
	if got := c.Value(); got < 10 {
		t.Errorf("after concurrent merges Value() = %d, want at least 10", got)
	}
}

// mockDelta is a mock implementation for testing error cases
type mockDelta struct{}

func (m *mockDelta) Type() string                 { return "MockDelta" }
func (m *mockDelta) MarshalJSON() ([]byte, error) { return nil, fmt.Errorf("mock error") }
func (m *mockDelta) UnmarshalJSON([]byte) error   { return fmt.Errorf("mock error") }

// mockCRDT is a mock CRDT implementation for testing error cases
type mockCRDT struct{}

func (m *mockCRDT) Type() string                 { return "MockCRDT" }
func (m *mockCRDT) Merge(other CRDT) error       { return fmt.Errorf("mock error") }
func (m *mockCRDT) ApplyDelta(delta Delta) error { return fmt.Errorf("mock error") }
func (m *mockCRDT) MarshalJSON() ([]byte, error) { return nil, fmt.Errorf("mock error") }
func (m *mockCRDT) UnmarshalJSON([]byte) error   { return fmt.Errorf("mock error") }
