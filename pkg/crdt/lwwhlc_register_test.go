package crdt

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Test compareHLC function
func TestCompareHLC(t *testing.T) {
	tests := []struct {
		name string
		a    HLCTimestamp
		b    HLCTimestamp
		want int
	}{
		{
			name: "a walltime < b walltime",
			a:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node1"},
			b:    HLCTimestamp{WallTime: 200, Lamport: 3, ID: "node2"},
			want: -1,
		},
		{
			name: "a walltime > b walltime",
			a:    HLCTimestamp{WallTime: 300, Lamport: 1, ID: "node1"},
			b:    HLCTimestamp{WallTime: 200, Lamport: 10, ID: "node2"},
			want: 1,
		},
		{
			name: "equal walltime, a lamport < b lamport",
			a:    HLCTimestamp{WallTime: 100, Lamport: 3, ID: "node1"},
			b:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node2"},
			want: -1,
		},
		{
			name: "equal walltime, a lamport > b lamport",
			a:    HLCTimestamp{WallTime: 100, Lamport: 7, ID: "node1"},
			b:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node2"},
			want: 1,
		},
		{
			name: "equal walltime and lamport, a ID < b ID",
			a:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node1"},
			b:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node2"},
			want: -1,
		},
		{
			name: "equal walltime and lamport, a ID > b ID",
			a:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node3"},
			b:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node2"},
			want: 1,
		},
		{
			name: "completely equal timestamps",
			a:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node1"},
			b:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node1"},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := compareHLC(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("compareHLC(%+v, %+v) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// Test basic Write and Read operations
func TestLWWHLCRegister_WriteRead(t *testing.T) {
	tests := []struct {
		name   string
		writes []json.RawMessage
		want   json.RawMessage
	}{
		{
			name:   "single write",
			writes: []json.RawMessage{json.RawMessage(`"hello"`)},
			want:   json.RawMessage(`"hello"`),
		},
		{
			name:   "multiple writes - last wins",
			writes: []json.RawMessage{json.RawMessage(`"first"`), json.RawMessage(`"second"`), json.RawMessage(`"third"`)},
			want:   json.RawMessage(`"third"`),
		},
		{
			name:   "write complex json",
			writes: []json.RawMessage{json.RawMessage(`{"key":"value","num":42}`)},
			want:   json.RawMessage(`{"key":"value","num":42}`),
		},
		{
			name:   "write null",
			writes: []json.RawMessage{json.RawMessage(`null`)},
			want:   json.RawMessage(`null`),
		},
		{
			name:   "overwrite with null",
			writes: []json.RawMessage{json.RawMessage(`"data"`), json.RawMessage(`null`)},
			want:   json.RawMessage(`null`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewLWWHLCRegister(uuid.New())

			// Perform writes with small delays to ensure HLC advances
			for i, w := range tc.writes {
				if i > 0 {
					time.Sleep(time.Millisecond)
				}
				r.Write(w)
			}

			got := r.Read()
			if string(got) != string(tc.want) {
				t.Errorf("Read() = %s, want %s", got, tc.want)
			}
		})
	}
}

// Test empty register
func TestLWWHLCRegister_EmptyRegister(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())
	got := r.Read()
	if got != nil {
		t.Errorf("Read() on empty register = %s, want nil", got)
	}
}

// Test ApplyDelta
func TestLWWHLCRegister_ApplyDelta(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func() (*LWWHLCRegister, Delta)
		wantValue json.RawMessage
		wantErr   bool
	}{
		{
			name: "apply valid delta",
			prepare: func() (*LWWHLCRegister, Delta) {
				r1 := NewLWWHLCRegister(uuid.New())
				r2 := NewLWWHLCRegister(uuid.New())
				time.Sleep(2 * time.Millisecond)
				delta := r2.Write(json.RawMessage(`"new value"`))
				return r1, delta
			},
			wantValue: json.RawMessage(`"new value"`),
			wantErr:   false,
		},
		{
			name: "apply older delta - should be ignored",
			prepare: func() (*LWWHLCRegister, Delta) {
				r1 := NewLWWHLCRegister(uuid.New())
				r2 := NewLWWHLCRegister(uuid.New())

				// r2 writes older value
				delta := r2.Write(json.RawMessage(`"older"`))

				// r1 writes newer value
				time.Sleep(2 * time.Millisecond)
				r1.Write(json.RawMessage(`"newer"`))

				return r1, delta
			},
			wantValue: json.RawMessage(`"newer"`),
			wantErr:   false,
		},
		{
			name: "apply invalid delta type",
			prepare: func() (*LWWHLCRegister, Delta) {
				r := NewLWWHLCRegister(uuid.New())
				return r, &mockDelta{}
			},
			wantValue: nil,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, delta := tc.prepare()
			err := r.ApplyDelta(delta)

			if (err != nil) != tc.wantErr {
				t.Fatalf("ApplyDelta() error = %v, wantErr %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			got := r.Read()
			if string(got) != string(tc.wantValue) {
				t.Errorf("Read() after ApplyDelta = %s, want %s", got, tc.wantValue)
			}
		})
	}
}

// Test Merge
func TestLWWHLCRegister_Merge(t *testing.T) {
	tests := []struct {
		name      string
		prepareA  func() *LWWHLCRegister
		prepareB  func() *LWWHLCRegister
		wantValue json.RawMessage
		wantErr   bool
	}{
		{
			name: "merge with newer value",
			prepareA: func() *LWWHLCRegister {
				r := NewLWWHLCRegister(uuid.New())
				r.Write(json.RawMessage(`"old"`))
				return r
			},
			prepareB: func() *LWWHLCRegister {
				r := NewLWWHLCRegister(uuid.New())
				time.Sleep(2 * time.Millisecond)
				r.Write(json.RawMessage(`"new"`))
				return r
			},
			wantValue: json.RawMessage(`"new"`),
			wantErr:   false,
		},
		{
			name: "merge into empty register",
			prepareA: func() *LWWHLCRegister {
				return NewLWWHLCRegister(uuid.New())
			},
			prepareB: func() *LWWHLCRegister {
				r := NewLWWHLCRegister(uuid.New())
				time.Sleep(2 * time.Millisecond)
				r.Write(json.RawMessage(`"value"`))
				return r
			},
			wantValue: json.RawMessage(`"value"`),
			wantErr:   false,
		},
		{
			name: "merge with wrong type",
			prepareA: func() *LWWHLCRegister {
				return NewLWWHLCRegister(uuid.New())
			},
			prepareB: func() *LWWHLCRegister {
				return nil
			},
			wantValue: nil,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.prepareA()
			b := tc.prepareB()

			var err error
			if b == nil {
				err = a.Merge(&mockCRDT{})
			} else {
				err = a.Merge(b)
			}

			if (err != nil) != tc.wantErr {
				t.Fatalf("Merge() error = %v, wantErr %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			got := a.Read()
			if string(got) != string(tc.wantValue) {
				t.Errorf("Read() after Merge = %s, want %s", got, tc.wantValue)
			}
		})
	}
}

// Test JSON serialization for register
func TestLWWHLCRegister_JSONSerialization(t *testing.T) {
	tests := []struct {
		name    string
		prepare func() *LWWHLCRegister
		wantErr bool
	}{
		{
			name: "register with value",
			prepare: func() *LWWHLCRegister {
				r := NewLWWHLCRegister(uuid.New())
				r.Write(json.RawMessage(`"test value"`))
				return r
			},
			wantErr: false,
		},
		{
			name: "register with complex json",
			prepare: func() *LWWHLCRegister {
				r := NewLWWHLCRegister(uuid.New())
				r.Write(json.RawMessage(`{"nested":{"key":"value"},"array":[1,2,3]}`))
				return r
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.prepare()
			originalValue := string(original.Read())

			// Marshal
			data, err := json.Marshal(original)
			if (err != nil) != tc.wantErr {
				t.Fatalf("MarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}

			// Unmarshal
			restored := &LWWHLCRegister{}
			if err := json.Unmarshal(data, restored); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}

			// Verify value is preserved
			got := string(restored.Read())
			if got != originalValue {
				t.Errorf("after unmarshal Read() = %s, want %s", got, originalValue)
			}

			// Verify ID is preserved
			if restored.id != original.id {
				t.Errorf("after unmarshal id = %q, want %q", restored.id, original.id)
			}
		})
	}
}

// Test Delta JSON serialization
func TestLWWHLCRegisterDelta_JSONSerialization(t *testing.T) {
	tests := []struct {
		name    string
		delta   *LWWHLCRegisterDelta
		wantErr bool
	}{
		{
			name: "valid delta",
			delta: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"test"`),
				TS: HLCTimestamp{
					WallTime: 123456789,
					Lamport:  5,
					ID:       "node1",
				},
			},
			wantErr: false,
		},
		{
			name: "delta with complex value",
			delta: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`{"key":"value"}`),
				TS: HLCTimestamp{
					WallTime: 987654321,
					Lamport:  10,
					ID:       "node2",
				},
			},
			wantErr: false,
		},
		{
			name: "delta with null value",
			delta: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`null`),
				TS: HLCTimestamp{
					WallTime: 111111111,
					Lamport:  0,
					ID:       "node3",
				},
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
			if raw["type"] != LWWHLCRegisterName {
				t.Errorf("type field = %q, want %q", raw["type"], LWWHLCRegisterName)
			}

			// Unmarshal
			restored := &LWWHLCRegisterDelta{}
			if err := json.Unmarshal(data, restored); err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}

			// Verify fields
			if string(restored.Value) != string(tc.delta.Value) {
				t.Errorf("Value = %s, want %s", restored.Value, tc.delta.Value)
			}
			if restored.TS != tc.delta.TS {
				t.Errorf("TS = %+v, want %+v", restored.TS, tc.delta.TS)
			}
		})
	}
}

// Test Delta.Type
func TestLWWHLCRegisterDelta_Type(t *testing.T) {
	d := &LWWHLCRegisterDelta{}
	if got := d.Type(); got != LWWHLCRegisterName {
		t.Errorf("Type() = %q, want %q", got, LWWHLCRegisterName)
	}
}

// Test Register.Type
func TestLWWHLCRegister_Type(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())
	if got := r.Type(); got != LWWHLCRegisterName {
		t.Errorf("Type() = %q, want %q", got, LWWHLCRegisterName)
	}
}

// Test Delta.Merge
func TestLWWHLCRegisterDelta_Merge(t *testing.T) {
	tests := []struct {
		name      string
		delta1    *LWWHLCRegisterDelta
		delta2    *LWWHLCRegisterDelta
		wantValue json.RawMessage
		wantErr   bool
	}{
		{
			name: "merge with newer delta",
			delta1: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"old"`),
				TS:    HLCTimestamp{WallTime: 100, Lamport: 1, ID: "node1"},
			},
			delta2: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"new"`),
				TS:    HLCTimestamp{WallTime: 200, Lamport: 1, ID: "node2"},
			},
			wantValue: json.RawMessage(`"new"`),
			wantErr:   false,
		},
		{
			name: "merge with older delta - keep current",
			delta1: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"newer"`),
				TS:    HLCTimestamp{WallTime: 300, Lamport: 5, ID: "node1"},
			},
			delta2: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"older"`),
				TS:    HLCTimestamp{WallTime: 200, Lamport: 3, ID: "node2"},
			},
			wantValue: json.RawMessage(`"newer"`),
			wantErr:   false,
		},
		{
			name: "merge with equal walltime, newer lamport",
			delta1: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"first"`),
				TS:    HLCTimestamp{WallTime: 100, Lamport: 3, ID: "node1"},
			},
			delta2: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"second"`),
				TS:    HLCTimestamp{WallTime: 100, Lamport: 5, ID: "node2"},
			},
			wantValue: json.RawMessage(`"second"`),
			wantErr:   false,
		},
		{
			name: "merge with wrong type",
			delta1: &LWWHLCRegisterDelta{
				Value: json.RawMessage(`"value"`),
				TS:    HLCTimestamp{WallTime: 100, Lamport: 1, ID: "node1"},
			},
			delta2:    nil,
			wantValue: json.RawMessage(`"value"`),
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			delta := &LWWHLCRegisterDelta{
				Value: append(json.RawMessage(nil), tc.delta1.Value...),
				TS:    tc.delta1.TS,
			}

			var err error
			if tc.delta2 == nil {
				err = delta.Merge(&mockDelta{})
			} else {
				err = delta.Merge(tc.delta2)
			}

			if (err != nil) != tc.wantErr {
				t.Fatalf("Merge() error = %v, wantErr %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if string(delta.Value) != string(tc.wantValue) {
				t.Errorf("after Merge Value = %s, want %s", delta.Value, tc.wantValue)
			}
		})
	}
}

// Test invalid delta type in UnmarshalJSON
func TestLWWHLCRegisterDelta_UnmarshalJSON_InvalidType(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "wrong type field",
			json:    `{"type":"WrongType","value":"test","ts":{"wall_time":100,"lamport":1,"id":"node1"}}`,
			wantErr: true,
		},
		{
			name:    "missing type field is ok",
			json:    `{"value":"test","ts":{"wall_time":100,"lamport":1,"id":"node1"}}`,
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
			var d LWWHLCRegisterDelta
			err := json.Unmarshal([]byte(tc.json), &d)
			if (err != nil) != tc.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// Test concurrent writes
func TestLWWHLCRegister_ConcurrentWrites(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			r.Write(json.RawMessage(fmt.Sprintf(`"%d"`, val)))
		}(i)
	}

	wg.Wait()

	// Should have some value without panicking
	got := r.Read()
	if got == nil {
		t.Error("Read() returned nil after concurrent writes")
	}
}

// Test concurrent reads
func TestLWWHLCRegister_ConcurrentReads(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())
	r.Write(json.RawMessage(`"test"`))

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Read()
		}()
	}

	wg.Wait()
}

// Test concurrent merges
func TestLWWHLCRegister_ConcurrentMerges(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())
	var wg sync.WaitGroup

	// Concurrent merges
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			other := NewLWWHLCRegister(uuid.New())
			other.Write(json.RawMessage(fmt.Sprintf(`"value_%d"`, val)))
			r.Merge(other)
		}(i)
	}

	wg.Wait()

	// Should have some value without panicking
	got := r.Read()
	if got == nil {
		t.Error("Read() returned nil after concurrent merges")
	}
}

// Test HLC advancement
func TestLWWHLCRegister_HLCAdvancement(t *testing.T) {
	r := NewLWWHLCRegister(uuid.New())

	// First write
	delta1 := r.Write(json.RawMessage(`"first"`))
	d1 := delta1.(*LWWHLCRegisterDelta)

	// Second write immediately after - should advance Lamport
	delta2 := r.Write(json.RawMessage(`"second"`))
	d2 := delta2.(*LWWHLCRegisterDelta)

	// The second write should have advanced timestamp
	cmp := compareHLC(d1.TS, d2.TS)
	if cmp >= 0 {
		t.Errorf("Second write timestamp should be greater than first, got %+v vs %+v", d2.TS, d1.TS)
	}

	// Wait and write again - WallTime should advance
	time.Sleep(5 * time.Millisecond)
	delta3 := r.Write(json.RawMessage(`"third"`))
	d3 := delta3.(*LWWHLCRegisterDelta)

	if d3.TS.WallTime <= d2.TS.WallTime {
		t.Errorf("Third write WallTime should be greater than second, got %d vs %d", d3.TS.WallTime, d2.TS.WallTime)
	}
}
