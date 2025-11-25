package buffer

import (
	mock_crdt "in-memorydb/pkg/crdt/mocks"
	"in-memorydb/pkg/structs"
	"strconv"
	"testing"

	"in-memorydb/pkg/storage"

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
// Assumes storage.Update has fields Key, NodeID and method Merge(*Update) error.
func (s *TestSuite) newUpdate(key, nodeID string) *storage.Update {

	crdtMock := mock_crdt.NewMockDelta(s.ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	return &storage.Update{
		Key:     key,
		NodeID:  nodeID,
		Payload: crdtMock,
	}
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

func (s *TestSuite) TestBuffer_LRU_EvictionAndOrder() {
	tests := []struct {
		name             string
		maxSize          int
		insertSequence   []struct{ key, node string } // order of Put
		expectedLen      int
		expectedOldest   struct{ key, node string } // expected PeekOldest after ops
		expectedAfterGet struct {
			// simulate Get on this key/node then expected oldest after that Get (to test move-to-front)
			getKey, getNode       string
			oldestKey, oldestNode string
		}
	}{
		{
			name:    "evict oldest when exceed maxSize",
			maxSize: 2,
			insertSequence: []struct{ key, node string }{
				{"A", "n1"}, // after first: [A]
				{"B", "n1"}, // [B,A]
				{"C", "n1"}, // [C,B] (A evicted)
			},
			expectedLen:    2,
			expectedOldest: struct{ key, node string }{"B", "n1"}, // B is oldest (back)
			expectedAfterGet: struct {
				getKey, getNode       string
				oldestKey, oldestNode string
			}{
				// if we Get(B) it should move to front -> remaining order [B,C] -> oldest C
				getKey: "B", getNode: "n1", oldestKey: "C", oldestNode: "n1",
			},
		},
		{
			name:    "inserting same key+node doesn't increase size (merge) and eviction correct",
			maxSize: 2,
			insertSequence: []struct{ key, node string }{
				{"A", "n1"}, // [A]
				{"A", "n1"}, // merge -> still [A]
				{"B", "n1"}, // [B,A]
				{"C", "n2"}, // [C,B] (A evicted)
			},
			expectedLen:    2,
			expectedOldest: struct{ key, node string }{"B", "n1"},
			expectedAfterGet: struct {
				getKey, getNode       string
				oldestKey, oldestNode string
			}{
				getKey: "B", getNode: "n1", oldestKey: "C", oldestNode: "n2",
			},
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			buf := NewBuffer(tc.maxSize)

			for _, op := range tc.insertSequence {
				buf.Put(s.newUpdate(op.key, op.node))
			}

			assert.Equal(t, tc.expectedLen, buf.Len(), "length after inserts should match")

			// check PeekOldest
			old, ok := buf.PeekOldest()
			assert.True(t, ok, "there should be an oldest element")
			assert.Equal(t, tc.expectedOldest.key, old.Key)
			assert.Equal(t, tc.expectedOldest.node, old.NodeID)

			// simulate Get that moves element to front, then check oldest again
			if tc.expectedAfterGet.getKey != "" {
				_, ok := buf.Get(tc.expectedAfterGet.getKey, tc.expectedAfterGet.getNode)
				assert.True(t, ok, "expected element to Get successfully")
				old2, ok2 := buf.PeekOldest()
				assert.True(t, ok2, "there should be an oldest element after Get")
				assert.Equal(t, tc.expectedAfterGet.oldestKey, old2.Key)
				assert.Equal(t, tc.expectedAfterGet.oldestNode, old2.NodeID)
			}
		})
	}
}

func (s *TestSuite) TestBuffer_Remove_PopOldest() {
	tests := []struct {
		name            string
		maxSize         int
		insertSeq       []struct{ key, node string }
		removeKey       string
		removeNode      string
		expectRemove    bool
		expectLenAfter  int
		expectPopOldest struct{ key, node string }
	}{
		{
			name:    "remove existing element",
			maxSize: 5,
			insertSeq: []struct{ key, node string }{
				{"k1", "n1"},
			},
			removeKey: "k1", removeNode: "n1",
			expectRemove:    true,
			expectLenAfter:  0,
			expectPopOldest: struct{ key, node string }{"", ""}, // none
		},
		{
			name:    "pop oldest returns correct element and reduces len",
			maxSize: 5,
			insertSeq: []struct{ key, node string }{
				{"A", "n1"},
				{"B", "n1"},
			},
			removeKey: "", removeNode: "",
			expectRemove:    false,
			expectLenAfter:  1, // after PopOldest
			expectPopOldest: struct{ key, node string }{"A", "n1"},
		},
	}

	for _, tc := range tests {
		s.T().Run(tc.name, func(t *testing.T) {
			buf := NewBuffer(tc.maxSize)
			for _, op := range tc.insertSeq {
				buf.Put(s.newUpdate(op.key, op.node))
			}

			if tc.removeKey != "" {
				removed := buf.Remove(tc.removeKey, tc.removeNode)
				assert.Equal(t, tc.expectRemove, removed)
				assert.Equal(t, tc.expectLenAfter, buf.Len())
			}

			// test PopOldest when expectedPopOldest.key set
			if tc.expectPopOldest.key != "" {
				u, ok := buf.PopOldest()
				assert.True(t, ok, "PopOldest expected to succeed")
				assert.NotNil(t, u)
				assert.Equal(t, tc.expectPopOldest.key, u.Key)
				assert.Equal(t, tc.expectPopOldest.node, u.NodeID)
				assert.Equal(t, tc.expectLenAfter, buf.Len())
			} else {
				// if none expected, PopOldest should return false (empty)
				// but only if len is zero
				if buf.Len() == 0 {
					_, ok := buf.PopOldest()
					assert.False(t, ok)
				}
			}
		})
	}
}

func (s *TestSuite) newUpdateWithRange(key string, node string, r structs.Range) *storage.Update {
	u := s.newUpdate(key, node)
	u.Range = r
	return u
}

func (s *TestSuite) TestBufferGetCovering() {
	buf := NewBuffer(10)
	u1 := s.newUpdateWithRange("A", "n1", structs.Range{Start: 0, End: 2})
	u2 := s.newUpdateWithRange("B", "n1", structs.Range{3, 4})
	u3 := s.newUpdateWithRange("C", "n1", structs.Range{5, 6})
	u4 := s.newUpdateWithRange("A", "n1", structs.Range{7, 10})
	u5 := s.newUpdateWithRange("A", "n1", structs.Range{11, 13})

	buf.Put(u1, u2, u3, u4, u5)
	covering := buf.GetCovering("n1", structs.Range{10, 12})
	s.Require().True(covering[0].Range.ContainsOther(structs.Range{10, 12}))
}

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
	s.Require().Equal([]*storage.Update{u1, u4, u3, u2}, items) // in reverse order because of lru politics

	buffer.RemoveN(3)
	items = buffer.PeekN(6)
	s.Require().Len(items, 1)
	s.Require().Equal([]*storage.Update{u1}, items)
}

func BenchmarkBuffer_Put(b *testing.B) {
	const maxSize = 1000
	buf := NewBuffer(maxSize)
	ctr := gomock.NewController(b)
	crdtMock := mock_crdt.NewMockDelta(ctr)
	crdtMock.EXPECT().Merge(gomock.Any()).Return(nil).AnyTimes()

	// Подготовка заранее: ключи и nodeId
	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	updates := make([]*storage.Update, b.N)
	for i := 0; i < b.N; i++ {
		k := "k" + strconv.Itoa(rand.Int()%maxSize)
		n := "n" + strconv.Itoa(rand.Int()%100)
		keys[i] = k
		nodes[i] = n
		updates[i] = &storage.Update{
			Key:     k,
			NodeID:  n,
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

	// Подготовка и заполнение буфера
	for i := 0; i < maxSize; i++ {
		k := "k" + strconv.Itoa(rand.Int()%maxSize)
		n := "n" + strconv.Itoa(rand.Int()%100)
		buf.Put(&storage.Update{
			Key:     k,
			NodeID:  n,
			Payload: crdtMock,
		})
	}

	// Подготовка ключей для Get
	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = "k" + strconv.Itoa(rand.Int()%maxSize)
		nodes[i] = "n" + strconv.Itoa(rand.Int()%100)
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

	// Подготовка заранее
	keys := make([]string, b.N)
	nodes := make([]string, b.N)
	updates := make([]*storage.Update, b.N)
	for i := 0; i < b.N; i++ {
		k := "k" + strconv.Itoa(rand.Int()%maxSize)
		n := "n" + strconv.Itoa(rand.Int()%100)
		keys[i] = k
		nodes[i] = n
		updates[i] = &storage.Update{
			Key:     keys[i],
			NodeID:  nodes[i],
			Payload: crdtMock,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Put(updates[i])
		_, _ = buf.Get(keys[i], nodes[i])
	}
}
