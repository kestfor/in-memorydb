package buffer

import (
	mock_crdt "in-memorydb/pkg/crdt/mocks"
	"in-memorydb/pkg/structs"
	"in-memorydb/pkg/types"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
	"golang.org/x/exp/rand"
)

type TestSuite struct {
	suite.Suite
	ctr *gomock.Controller
}

func TestTestSuite(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func (s *TestSuite) SetupSuite() {
	s.ctr = gomock.NewController(s.T())
}

// helper to build storage.Update quickly in tests.
func (s *TestSuite) newUpdate(key, nodeID string) *types.Update {
	crdtMock := mock_crdt.NewMockDelta(s.ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	return &types.Update{
		Key:     key,
		NodeID:  nodeID,
		Payload: crdtMock,
	}
}

func (s *TestSuite) newUpdateWithRange(key string, node string, r structs.Range) *types.Update {
	u := s.newUpdate(key, node)
	u.Range = r
	return u
}

func (s *TestSuite) TestBuffer_Put_Len() {
	tests := []struct {
		name    string
		maxSize int
		ops     []struct {
			key, node string
			r         structs.Range
		}
		expLen     int
		expPresent []struct{ key, node string }
	}{
		{
			name:    "put same key+node twice with overlapping ranges -> merged, len stays 1",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
				{"k1", "n1", structs.Range{5, 15}},
			},
			expLen: 1,
			expPresent: []struct{ key, node string }{
				{"k1", "n1"},
			},
		},
		{
			name:    "put same key+node twice with non-overlapping ranges -> len 2",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
				{"k1", "n1", structs.Range{12, 15}},
			},
			expLen: 2,
			expPresent: []struct{ key, node string }{
				{"k1", "n1"},
			},
		},
		{
			name:    "put same key different node -> two entries",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
				{"k1", "n2", structs.Range{0, 10}},
			},
			expLen: 2,
			expPresent: []struct{ key, node string }{
				{"k1", "n1"},
				{"k1", "n2"},
			},
		},
		{
			name:    "different keys different nodes -> multiple entries",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
				{"k2", "n1", structs.Range{0, 10}},
				{"k3", "n2", structs.Range{0, 10}},
			},
			expLen: 3,
			expPresent: []struct{ key, node string }{
				{"k1", "n1"},
				{"k2", "n1"},
				{"k3", "n2"},
			},
		},
		{
			name:    "exceed maxSize triggers eviction",
			maxSize: 2,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
				{"k2", "n1", structs.Range{0, 10}},
				{"k3", "n1", structs.Range{0, 10}},
			},
			expLen: 2,
			expPresent: []struct{ key, node string }{
				{"k2", "n1"},
				{"k3", "n1"},
			},
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			buf := NewBuffer(tc.maxSize)

			for _, op := range tc.ops {
				buf.Put(s.newUpdateWithRange(op.key, op.node, op.r))
			}

			assert.Equal(t, tc.expLen, buf.Len(), "unexpected buffer length")

			// ensure expected entries present via Get
			for _, p := range tc.expPresent {
				us, ok := buf.Get(p.key, p.node)
				assert.True(t, ok, "entry should be present %s/%s", p.key, p.node)
				assert.NotEmpty(t, us)
			}
		})
	}
}

func (s *TestSuite) TestBuffer_Put_Collapse() {
	tests := []struct {
		name    string
		maxSize int
		ops     []struct {
			key, node string
			r         structs.Range
		}
		expUpdates []struct {
			key, node string
			r         structs.Range
		}
		expItemCount int
	}{
		{
			name:    "merge overlapping and adjacent ranges",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 9}},
				{"k1", "n1", structs.Range{11, 13}},
				{"k1", "n1", structs.Range{10, 10}},
				{"k1", "n1", structs.Range{12, 12}},
			},
			expUpdates: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 13}},
			},
			expItemCount: 1,
		},
		{
			name:    "non-merging ranges stay separate",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 5}},
				{"k1", "n1", structs.Range{7, 10}},
				{"k1", "n1", structs.Range{12, 15}},
			},
			expUpdates: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 5}},
				{"k1", "n1", structs.Range{7, 10}},
				{"k1", "n1", structs.Range{12, 15}},
			},
			expItemCount: 3,
		},
		{
			name:    "merge multiple overlapping segments",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 5}},
				{"k1", "n1", structs.Range{3, 8}},
				{"k1", "n1", structs.Range{6, 10}},
			},
			expUpdates: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 10}},
			},
			expItemCount: 1,
		},
		{
			name:    "adjacent ranges merge (end+1 == start)",
			maxSize: 10,
			ops: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 4}},
				{"k1", "n1", structs.Range{5, 9}},
			},
			expUpdates: []struct {
				key  string
				node string
				r    structs.Range
			}{
				{"k1", "n1", structs.Range{0, 9}},
			},
			expItemCount: 1,
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			buf := NewBuffer(tc.maxSize)

			for _, op := range tc.ops {
				buf.Put(s.newUpdateWithRange(op.key, op.node, op.r))
			}

			us, ok := buf.Get("k1", "n1")
			assert.True(t, ok)
			assert.Len(t, us, len(tc.expUpdates))

			for i, exp := range tc.expUpdates {
				assert.Equal(t, exp.r, us[i].Range)
			}

			assert.Equal(t, tc.expItemCount, buf.Len())
		})
	}
}

//func (s *TestSuite) TestBufferGetCovering() {
//	tests := []struct {
//		name    string
//		maxSize int
//		setup   []struct {
//			key, node string
//			r         structs.Range
//		}
//		queryNode  string
//		queryRange structs.Range
//		expCount   int
//		validate   func(*testing.T, []*storage.Update)
//	}{
//		{
//			name:    "basic overlap detection",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{0, 2}},
//				{"B", "n1", structs.Range{3, 4}},
//				{"C", "n1", structs.Range{5, 6}},
//				{"A", "n1", structs.Range{7, 10}},
//				{"A", "n1", structs.Range{11, 13}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{10, 12},
//			expCount:   2,
//			validate: func(t *testing.T, updates []*storage.Update) {
//				assert.True(t, updates[0].Range.Start <= 12 && updates[0].Range.End >= 10)
//				assert.True(t, updates[1].Range.Start <= 12 && updates[1].Range.End >= 10)
//			},
//		},
//		{
//			name:    "no overlap",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{0, 5}},
//				{"B", "n1", structs.Range{10, 15}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{20, 25},
//			expCount:   0,
//		},
//		{
//			name:    "query covers all ranges",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{5, 10}},
//				{"B", "n1", structs.Range{15, 20}},
//				{"C", "n1", structs.Range{25, 30}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{0, 100},
//			expCount:   3,
//		},
//		{
//			name:    "exact match",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{10, 20}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{10, 20},
//			expCount:   1,
//		},
//		{
//			name:    "partial overlaps",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{0, 10}},
//				{"B", "n1", structs.Range{5, 15}},
//				{"C", "n1", structs.Range{12, 20}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{8, 18},
//			expCount:   3,
//		},
//		{
//			name:    "different nodeID returns empty",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{0, 10}},
//				{"B", "n2", structs.Range{5, 15}},
//			},
//			queryNode:  "n3",
//			queryRange: structs.Range{0, 20},
//			expCount:   0,
//		},
//		{
//			name:    "edge case: single point range",
//			maxSize: 10,
//			setup: []struct {
//				key  string
//				node string
//				r    structs.Range
//			}{
//				{"A", "n1", structs.Range{5, 5}},
//				{"B", "n1", structs.Range{10, 10}},
//			},
//			queryNode:  "n1",
//			queryRange: structs.Range{5, 10},
//			expCount:   2,
//		},
//	}
//
//	for _, tc := range tests {
//		s.T().Run(tc.name, func(t *testing.T) {
//			buf := NewBuffer(tc.maxSize)
//
//			var updates []*storage.Update
//			for _, op := range tc.setup {
//				u := s.newUpdateWithRange(op.key, op.node, op.r)
//				updates = append(updates, u)
//			}
//			buf.Put(updates...)
//
//			covering := buf.GetCovering(tc.queryNode, tc.queryRange)
//			assert.Len(t, covering, tc.expCount, "unexpected number of covering updates")
//
//			if tc.validate != nil {
//				tc.validate(t, covering)
//			}
//		})
//	}
//}

func (s *TestSuite) TestBuffer_PeekN() {
	buffer := NewBuffer(10)
	u1 := s.newUpdate("k1", "n1")
	u2 := s.newUpdate("k2", "n1")
	u3 := s.newUpdate("k3", "n1")
	u4 := s.newUpdate("k4", "n1")
	u5 := s.newUpdate("k1", "n1") // merged with first
	buffer.Put(u1)
	buffer.Put(u2)
	buffer.Put(u3)
	buffer.Put(u4)
	buffer.Put(u5)

	items := buffer.PeekN(6)
	s.Require().Len(items, 4)
	s.Require().Equal([]*types.Update{u1, u4, u3, u2}, items) // in reverse order because of lru politics

	buffer.RemoveN(3)
	items = buffer.PeekN(6)
	s.Require().Len(items, 1)
	s.Require().Equal([]*types.Update{u1}, items)
}

func (s *TestSuite) TestBuffer_Remove() {
	tests := []struct {
		name       string
		maxSize    int
		setup      []struct{ key, node string }
		removeKey  string
		removeNode string
		expRemoved bool
		expLen     int
	}{
		{
			name:    "remove existing entry",
			maxSize: 10,
			setup: []struct{ key, node string }{
				{"k1", "n1"},
				{"k2", "n1"},
			},
			removeKey:  "k1",
			removeNode: "n1",
			expRemoved: true,
			expLen:     1,
		},
		{
			name:    "remove non-existing entry",
			maxSize: 10,
			setup: []struct{ key, node string }{
				{"k1", "n1"},
			},
			removeKey:  "k2",
			removeNode: "n1",
			expRemoved: false,
			expLen:     1,
		},
		{
			name:    "remove last entry makes buffer empty",
			maxSize: 10,
			setup: []struct{ key, node string }{
				{"k1", "n1"},
			},
			removeKey:  "k1",
			removeNode: "n1",
			expRemoved: true,
			expLen:     0,
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			buf := NewBuffer(tc.maxSize)
			for _, op := range tc.setup {
				buf.Put(s.newUpdate(op.key, op.node))
			}

			removed := buf.Remove(tc.removeKey, tc.removeNode)
			assert.Equal(t, tc.expRemoved, removed)
			assert.Equal(t, tc.expLen, buf.Len())
		})
	}
}

func (s *TestSuite) TestBuffer_RemoveN() {
	buf := NewBuffer(10)
	for i := 0; i < 5; i++ {
		buf.Put(s.newUpdate("k"+strconv.Itoa(i), "n1"))
	}

	removed := buf.RemoveN(3)
	s.Require().Equal(3, removed)
	s.Require().Equal(2, buf.Len())

	// Remove more than available
	removed = buf.RemoveN(10)
	s.Require().Equal(2, removed)
	s.Require().Equal(0, buf.Len())
}

// Benchmarks

func BenchmarkBuffer_Put(b *testing.B) {
	const maxSize = 1000
	buf := NewBuffer(maxSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	updates := make([]*types.Update, b.N)
	for i := 0; i < b.N; i++ {
		k := "k" + strconv.Itoa(rand.Int()%maxSize)
		n := "n" + strconv.Itoa(rand.Int()%100)
		keys[i] = k
		nodes[i] = n
		updates[i] = &types.Update{
			Key:     k,
			NodeID:  n,
			Range:   structs.Range{Start: uint64(rand.Int() % 1000), End: uint64(rand.Int()%1000 + 1000)},
			Payload: crdtMock,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Put(updates[i])
	}
}

func BenchmarkBuffer_Get(b *testing.B) {
	const maxSize = 1000
	buf := NewBuffer(maxSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	// Fill buffer
	for i := 0; i < maxSize; i++ {
		k := "k" + strconv.Itoa(rand.Int()%10000)
		n := "n" + strconv.Itoa(rand.Int()%10000)
		buf.Put(&types.Update{
			Key:     k,
			NodeID:  n,
			Range:   structs.Range{Start: uint64(rand.Int() % 1000), End: uint64(rand.Int()%1000 + 1000)},
			Payload: crdtMock,
		})
	}

	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = "k" + strconv.Itoa(rand.Int()%10000)
		nodes[i] = "n" + strconv.Itoa(rand.Int()%10000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = buf.Get(keys[i], nodes[i])
	}
}

func BenchmarkBuffer_PutAndGet(b *testing.B) {
	const maxSize = 1000
	buf := NewBuffer(maxSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	updates := make([]*types.Update, b.N)
	for i := 0; i < b.N; i++ {
		k := "k" + strconv.Itoa(rand.Int()%10000)
		n := "n" + strconv.Itoa(rand.Int()%10000)
		keys[i] = k
		nodes[i] = n
		updates[i] = &types.Update{
			Key:     keys[i],
			NodeID:  nodes[i],
			Range:   structs.Range{Start: uint64(rand.Int() % 1000), End: uint64(rand.Int()%1000 + 1000)},
			Payload: crdtMock,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Put(updates[i])
		_, _ = buf.Get(keys[i], nodes[i])
	}
}

func BenchmarkBuffer_GetCovering(b *testing.B) {
	benchmarks := []struct {
		name       string
		bufferSize int
		numNodes   int
		numUpdates int
		rangeWidth int
		queryWidth int
	}{
		{
			name:       "small buffer, narrow queries",
			bufferSize: 100,
			numNodes:   5,
			numUpdates: 100,
			rangeWidth: 100,
			queryWidth: 50,
		},
		{
			name:       "medium buffer, medium queries",
			bufferSize: 1000,
			numNodes:   20,
			numUpdates: 1000,
			rangeWidth: 500,
			queryWidth: 200,
		},
		{
			name:       "large buffer, wide queries",
			bufferSize: 10000,
			numNodes:   50,
			numUpdates: 10000,
			rangeWidth: 1000,
			queryWidth: 500,
		},
		{
			name:       "many nodes, sparse ranges",
			bufferSize: 5000,
			numNodes:   100,
			numUpdates: 5000,
			rangeWidth: 50,
			queryWidth: 100,
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			buf := NewBuffer(bm.bufferSize)
			ctr := gomock.NewController(b)
			crdtMock := mock_crdt.NewMockDelta(ctr)
			crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

			// Prepare node IDs
			nodes := make([]string, bm.numNodes)
			for i := 0; i < bm.numNodes; i++ {
				nodes[i] = "n" + strconv.Itoa(i)
			}

			// Fill buffer with random updates
			for i := 0; i < bm.numUpdates; i++ {
				nodeID := nodes[rand.Int()%bm.numNodes]
				start := uint64(rand.Int() % 10000)
				end := start + uint64(rand.Int()%bm.rangeWidth)

				buf.Put(&types.Update{
					Key:     "k" + strconv.Itoa(rand.Int()%1000),
					NodeID:  nodeID,
					Range:   structs.Range{Start: start, End: end},
					Payload: crdtMock,
				})
			}

			// Prepare query ranges
			queries := make([]struct {
				nodeID string
				r      structs.Range
			}, b.N)

			for i := 0; i < b.N; i++ {
				nodeID := nodes[rand.Int()%bm.numNodes]
				start := uint64(rand.Int() % 10000)
				end := start + uint64(rand.Int()%bm.queryWidth)
				queries[i].nodeID = nodeID
				queries[i].r = structs.Range{Start: start, End: end}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = buf.GetCovering(queries[i].nodeID, queries[i].r)
			}
		})
	}
}

func BenchmarkBuffer_GetCovering_WorstCase(b *testing.B) {
	// Worst case: all updates overlap with every query
	const bufferSize = 1000
	const numNodes = 10

	buf := NewBuffer(bufferSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "n" + strconv.Itoa(i)
	}

	// Create overlapping ranges (all cover 0-10000)
	for i := 0; i < bufferSize; i++ {
		nodeID := nodes[i%numNodes]
		buf.Put(&types.Update{
			Key:     "k" + strconv.Itoa(i),
			NodeID:  nodeID,
			Range:   structs.Range{Start: 0, End: 10000},
			Payload: crdtMock,
		})
	}

	// Query that overlaps with everything
	queryRange := structs.Range{Start: 5000, End: 6000}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := nodes[i%numNodes]
		_ = buf.GetCovering(nodeID, queryRange)
	}
}

func BenchmarkBuffer_GetCovering_BestCase(b *testing.B) {
	// Best case: no overlaps
	const bufferSize = 1000
	const numNodes = 10

	buf := NewBuffer(bufferSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	nodes := make([]string, numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = "n" + strconv.Itoa(i)
	}

	// Create non-overlapping ranges
	for i := 0; i < bufferSize; i++ {
		nodeID := nodes[i%numNodes]
		start := uint64(i * 100)
		end := start + 50
		buf.Put(&types.Update{
			Key:     "k" + strconv.Itoa(i),
			NodeID:  nodeID,
			Range:   structs.Range{Start: start, End: end},
			Payload: crdtMock,
		})
	}

	// Query that doesn't overlap with anything
	queryRange := structs.Range{Start: 500000, End: 500100}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeID := nodes[i%numNodes]
		_ = buf.GetCovering(nodeID, queryRange)
	}
}
