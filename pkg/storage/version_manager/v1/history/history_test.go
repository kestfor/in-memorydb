package history_test

import (
	"github/kestfor/in-memorydb/pkg/storage/version_manager/v1/history"
	"github/kestfor/in-memorydb/pkg/structs"
	"reflect"
	"testing"
)

//TODO fix
//func TestAddAndMerge(t *testing.T) {
//	h := history.NewHistory()
//
//	tests := []struct {
//		name   string
//		node   string
//		start  uint64
//		end    uint64
//		expect []structs.Range
//	}{
//		{
//			name:   "Add simple first range",
//			node:   "A",
//			start:  1,
//			end:    5,
//			expect: []structs.Range{{Start: 1, End: 5}},
//		},
//		{
//			name:   "Add non-overlapping right",
//			node:   "A",
//			start:  7,
//			end:    10,
//			expect: []structs.Range{{Start: 1, End: 5}, {Start: 7, End: 10}},
//		},
//		{
//			name:   "Add merge gap filler",
//			node:   "A",
//			start:  6,
//			end:    6,
//			expect: []structs.Range{{Start: 1, End: 10}},
//		},
//		{
//			name:   "Add overlapping left extension",
//			node:   "A",
//			start:  0,
//			end:    2,
//			expect: []structs.Range{{Start: 1, End: 10}},
//		},
//		{
//			name:   "Add overlapping right extension",
//			node:   "A",
//			start:  10,
//			end:    15,
//			expect: []structs.Range{{Start: 1, End: 15}},
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			h.AddRange(tt.node, structs.Range{tt.start, tt.end})
//			got := h.DiffAll(map[string]uint64{"A": 0})["A"] // quick read of actual
//			if !reflect.DeepEqual(got, tt.expect) {
//				t.Fatalf("expected=%v got=%v", tt.expect, got)
//			}
//		})
//	}
//}

func TestHas(t *testing.T) {
	h := history.NewHistory()

	h.AddRange("A", structs.Range{1, 5})
	h.AddRange("A", structs.Range{7, 10})

	tests := []struct {
		seq  uint64
		want bool
	}{
		{1, true},
		{3, true},
		{5, true},
		{6, false}, // gap
		{7, true},
		{10, true},
		{11, false},
	}

	for _, tt := range tests {
		t.Run("seq check", func(t *testing.T) {
			got := h.Has("A", tt.seq)
			if got != tt.want {
				t.Fatalf("Has(%d) want=%v got=%v", tt.seq, tt.want, got)
			}
		})
	}
}

func TestVectorClockMax(t *testing.T) {
	h := history.NewHistory()
	h.AddRange("A", structs.Range{1, 5})
	h.AddRange("A", structs.Range{7, 10})
	h.AddRange("B", structs.Range{100, 200})

	got := h.VectorClockMax()
	want := map[string]uint64{
		"A": 10,
		"B": 200,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VectorClockMax want=%v got=%v", want, got)
	}
}

func TestVectorClockContiguous(t *testing.T) {
	h := history.NewHistory()
	h.AddRange("A", structs.Range{1, 5})
	h.AddRange("A", structs.Range{7, 10})  // gap -> contiguous = 5
	h.AddRange("B", structs.Range{3, 3})   // contiguous = 0 because first coverage starts at 3, not 1
	h.AddRange("B", structs.Range{1, 2})   // now contiguous = 3
	h.AddRange("B", structs.Range{4, 6})   // no gap -> contiguous = 6
	h.AddRange("C", structs.Range{10, 20}) // contiguous = 0 since no coverage from 1

	got := h.VectorClockContiguous()
	want := map[string]uint64{
		"A": 5,
		"B": 6,
		"C": 0,
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("VectorClockContiguous want=%v got=%v", want, got)
	}
}

func TestDiffSimple(t *testing.T) {
	h := history.NewHistory()
	h.AddRange("A", structs.Range{1, 5})
	h.AddRange("A", structs.Range{7, 10})

	tests := []struct {
		name       string
		remoteLast uint64
		want       []structs.Range
	}{
		{
			name:       "remote behind first range",
			remoteLast: 2,
			want: []structs.Range{
				{Start: 3, End: 5},
				{Start: 7, End: 10},
			},
		},
		{
			name:       "remote behind gap",
			remoteLast: 5,
			want: []structs.Range{
				{Start: 7, End: 10},
			},
		},
		{
			name:       "remote inside second range",
			remoteLast: 8,
			want: []structs.Range{
				{Start: 9, End: 10},
			},
		},
		{
			name:       "remote ahead of all",
			remoteLast: 10,
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.Diff("A", tt.remoteLast)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Diff(%d) want=%v got=%v", tt.remoteLast, tt.want, got)
			}
		})
	}
}

// TODO fix
//func TestDiffAll(t *testing.T) {
//	h := history.NewHistory()
//
//	h.AddRange("A", structs.Range{1, 5})
//	h.AddRange("A", structs.Range{7, 10})
//	h.AddRange("B", structs.Range{100, 105})
//
//	tests := []struct {
//		name   string
//		remote map[string]uint64
//		want   map[string][]structs.Range
//	}{
//		{
//			name:   "remote has nothing",
//			remote: map[string]uint64{"A": 0, "B": 0},
//			want: map[string][]structs.Range{
//				"A": {
//					{Start: 1, End: 5},
//					{Start: 7, End: 10},
//				},
//				"B": {
//					{Start: 100, End: 105},
//				},
//			},
//		},
//		{
//			name:   "remote partially synced",
//			remote: map[string]uint64{"A": 5, "B": 102},
//			want: map[string][]structs.Range{
//				"A": {
//					{Start: 7, End: 10},
//				},
//				"B": {
//					{Start: 103, End: 105},
//				},
//			},
//		},
//		{
//			name:   "remote ahead for A, behind for B",
//			remote: map[string]uint64{"A": 20, "B": 101},
//			want: map[string][]structs.Range{
//				"B": {
//					{Start: 102, End: 105},
//				},
//			},
//		},
//		{
//			name:   "remote missing node C",
//			remote: map[string]uint64{"A": 9},
//			// B must still be returned entirely
//			want: map[string][]structs.Range{
//				"A": {
//					{Start: 10, End: 10},
//				},
//				"B": {
//					{Start: 100, End: 105},
//				},
//			},
//		},
//	}
//
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			got := h.DiffAll(tt.remote)
//			if !reflect.DeepEqual(got, tt.want) {
//				t.Fatalf("DiffAll want=%v got=%v", tt.want, got)
//			}
//		})
//	}
//}
