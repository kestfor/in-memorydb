package updates_buffer

import (
	mock_crdt "in-memorydb/pkg/crdt/mocks"
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

func (s *TestSuite) TestBuffer_Put_MergeAndLen() {

	tests := []struct {
		name       string
		maxSize    int
		ops        []struct{ key, node string }
		expLen     int
		expPresent []struct{ key, node string }
	}{
		{
			name:    "put same key+node twice -> merged, len stays 1",
			maxSize: 10,
			ops: []struct{ key, node string }{
				{"k1", "n1"},
				{"k1", "n1"},
			},
			expLen: 1,
			expPresent: []struct{ key, node string }{
				{"k1", "n1"},
			},
		},
		{
			name:    "put same key different node -> two entries",
			maxSize: 10,
			ops: []struct{ key, node string }{
				{"k1", "n1"},
				{"k1", "n2"},
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
			ops: []struct{ key, node string }{
				{"k1", "n1"},
				{"k2", "n1"},
				{"k3", "n2"},
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
				buf.Put(s.newUpdate(op.key, op.node))
			}

			assert.Equal(t, tc.expLen, buf.Len(), "unexpected buffer length")

			// ensure expected entries present via Get
			for _, p := range tc.expPresent {
				u, ok := buf.Get(p.key, p.node)
				assert.True(t, ok, "entry should be present %s/%s", p.key, p.node)
				assert.NotNil(t, u)
				if u != nil {
					assert.Equal(t, p.key, u.Key)
					assert.Equal(t, p.node, u.NodeID)
				}
			}
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
