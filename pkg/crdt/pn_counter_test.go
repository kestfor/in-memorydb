package crdt

import (
	"sort"
	"testing"
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := NewPNCounter[string, int64]("nodeA")
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
		prepareA func() *PNCounter[string, int64]
		prepareB func() *PNCounter[string, int64]
		want     int64
	}{
		{
			name: "merge disjoint nodes",
			prepareA: func() *PNCounter[string, int64] {
				a := NewPNCounter[string, int64]("A")
				a.Increment(3)
				a.Decrement(1)
				return a
			},
			prepareB: func() *PNCounter[string, int64] {
				b := NewPNCounter[string, int64]("B")
				b.Increment(4)
				b.Decrement(2)
				return b
			},
			want: 4, // (3+4)-(1+2) = 4
		},
		{
			name: "merge same node takes max per entry",
			prepareA: func() *PNCounter[string, int64] {
				a := NewPNCounter[string, int64]("A")
				a.Increment(2)
				a.Decrement(1)
				return a
			},
			prepareB: func() *PNCounter[string, int64] {
				b := NewPNCounter[string, int64]("A")
				b.Increment(5) // larger than a.P["A"]
				b.Decrement(3) // larger than a.N["A"]
				return b
			},
			want: 2, // 5 - 3 = 2
		},
		{
			name: "merge mixture of same and disjoint nodes",
			prepareA: func() *PNCounter[string, int64] {
				a := NewPNCounter[string, int64]("A")
				a.Increment(1)
				return a
			},
			prepareB: func() *PNCounter[string, int64] {
				b := NewPNCounter[string, int64]("B")
				b.Increment(7)
				// also B has some count for A that's higher than A's
				// simulate by creating a counter with id A and then merging into b
				tmp := NewPNCounter[string, int64]("A")
				tmp.Increment(4)
				// merge tmp into b so b knows about node "A"
				b.Merge(tmp)
				return b
			},
			want: (4) + (7) - 0, // A: max(1,4)=4; B:7 => total 11
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

func TestPNCounter_SnapshotAndRestore(t *testing.T) {
	// build counter with two nodes by merging
	a := NewPNCounter[string, int64]("A")
	a.Increment(3)
	a.Decrement(1)

	b := NewPNCounter[string, int64]("B")
	b.Increment(5)
	b.Decrement(2)

	// merge B into A to produce both node entries in A
	a.Merge(b)

	// snapshot
	snap, err := a.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error: %v", err)
	}

	// restore
	restored, err := NewPNCounterFromSnapshot[string, int64](snap)
	if err != nil {
		t.Fatalf("NewPNCounterFromSnapshot error: %v", err)
	}

	// check value equality
	if got := restored.Value(); got != a.Value() {
		t.Fatalf("restored Value()=%d, want %d", got, a.Value())
	}

	// check IDs contain both A and B
	idsSet := restored.IDs()
	ids := idsSet.Slice() // expecting []string
	gotIDs := sortedStrings(ids)
	wantIDs := sortedStrings([]string{"A", "B"})
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("restored IDs mismatch: got %v want %v", gotIDs, wantIDs)
	}
	for i := range gotIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("restored IDs mismatch: got %v want %v", gotIDs, wantIDs)
		}
	}
}

func TestPNCounter_NewFromSnapshot_Error(t *testing.T) {
	// invalid JSON should return error
	_, err := NewPNCounterFromSnapshot[string, int64]([]byte("not valid json"))
	if err == nil {
		t.Fatalf("expected error from NewPNCounterFromSnapshot with invalid json, got nil")
	}
}
