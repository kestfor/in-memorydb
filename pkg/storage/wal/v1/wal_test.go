package wal

import (
	"context"
	"in-memorydb/pkg/crdt"
	. "in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	types2 "in-memorydb/pkg/types"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var typedNil crdt.Delta = &crdt.PNCounterDelta{}

type WALSuite struct {
	suite.Suite
	wal WAL
	dir string
}

func (s *WALSuite) SetupTest() {
	tmpDir, err := os.MkdirTemp("", "wal_test")
	s.Require().NoError(err)
	s.dir = tmpDir

	w, err := Open(s.dir)
	s.Require().NoError(err)
	s.wal = w
}

func (s *WALSuite) TearDownTest() {
	if s.wal != nil {
		s.Require().NoError(s.wal.Close())
	}
	s.Require().NoError(os.RemoveAll(s.dir))
}
func (s *WALSuite) TestAppendAndGet() {
	tests := []struct {
		node string
		seq  uint64
		data []byte
	}{
		{"A", 1, []byte("hello")},
		{"A", 2, []byte("world")},
		{"B", 1, []byte("bbb")},
	}

	for _, tt := range tests {

		u := &types2.Update{
			NodeID:  tt.node,
			Range:   structs.Range{Start: tt.seq, End: tt.seq},
			Payload: typedNil,
			Type:    types2.UpdateTypeDelete,
		}

		err := s.wal.Append(context.Background(), u)
		require.NoError(s.T(), err)
	}

	for _, tt := range tests {
		u, err := s.wal.Get(tt.node, tt.seq)
		require.NoError(s.T(), err)
		require.Equal(s.T(), tt.node, u.NodeID)
		require.Equal(s.T(), tt.seq, u.Range.Start)
	}
}

func (s *WALSuite) TestReplay() {
	_ = s.wal.Append(context.Background(), &types2.Update{NodeID: "A", Range: structs.Range{Start: 1, End: 1}, Payload: typedNil, Type: types2.UpdateTypeDelete})
	_ = s.wal.Append(context.Background(), &types2.Update{NodeID: "A", Range: structs.Range{Start: 2, End: 2}, Payload: typedNil, Type: types2.UpdateTypeDelete})
	_ = s.wal.Append(context.Background(), &types2.Update{NodeID: "A", Range: structs.Range{Start: 3, End: 3}, Payload: typedNil, Type: types2.UpdateTypeDelete})

	var seqs []uint64

	err := s.wal.Replay(context.Background(), "A", 2, func(u *types2.Update) error {
		seqs = append(seqs, u.Range.Start)
		return nil
	})

	require.NoError(s.T(), err)

	require.Equal(s.T(), []uint64{2, 3}, seqs)
}

func TestWALSuite(t *testing.T) {
	suite.Run(t, new(WALSuite))
}

func BenchmarkAppendSequential(b *testing.B) {
	b.ReportAllocs()

	dir := os.TempDir()
	w, err := Open(dir)
	if err != nil {
		b.Fatalf("Open WAL: %v", err)
	}

	defer os.RemoveAll(dir)
	defer w.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {

		u := &types2.Update{NodeID: "node-" + strconv.Itoa(i%16), Range: structs.Range{Start: uint64(i), End: uint64(i)}, Payload: typedNil, Type: types2.UpdateTypeDelete}
		if err := w.Append(context.Background(), u); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}

}

//func BenchmarkGetSequential(b *testing.B) {
//	b.ReportAllocs()
//
//	const numNodes = 10
//	const perNode = 10000
//	dir := b.TempDir()
//	w, err := Open(dir)
//	if err != nil {
//		b.Fatalf("Open WAL: %v", err)
//	}
//	defer func() { _ = w.Close() }()
//
//	// подготовка: для каждого node создаём perNode записей с SeqNum 1..perNode
//	for n := 0; n < numNodes; n++ {
//		nodeID := fmt.Sprintf("node-%02d", n)
//		for s := 1; s <= perNode; s++ {
//			if err := w.Append(&Entry{
//				NodeID:  nodeID,
//				SeqNum:  uint64(s),
//				Payload: []byte("payload"),
//			}); err != nil {
//				b.Fatalf("setup Append: %v", err)
//			}
//		}
//	}
//
//	b.ResetTimer()
//	for i := 0; i < b.N; i++ {
//		nodeIdx := i % numNodes
//		seq := uint64((i % perNode) + 1)
//		nodeID := fmt.Sprintf("node-%02d", nodeIdx)
//		e, err := w.Get(nodeID, seq)
//		if err != nil {
//			b.Fatalf("Get: %v", err)
//		}
//		// небольшая sanity-проверка, чтобы компилятор не оптимизировал вызов Get
//		if e == nil || e.SeqNum != seq {
//			b.Fatalf("Get returned wrong entry: got %+v want seq=%d", e, seq)
//		}
//	}
//}
//
//func BenchmarkReplay(b *testing.B) {
//	b.ReportAllocs()
//
//	const perNode = 50000
//	dir := b.TempDir()
//	w, err := Open(dir)
//	if err != nil {
//		b.Fatalf("Open WAL: %v", err)
//	}
//	defer func() { _ = w.Close() }()
//
//	nodeID := "replay-node"
//	// подготовка: много записей для одного узла
//	for s := 1; s <= perNode; s++ {
//		if err := w.Append(&Entry{
//			NodeID:  nodeID,
//			SeqNum:  uint64(s),
//			Payload: []byte("payload"),
//		}); err != nil {
//			b.Fatalf("setup Append: %v", err)
//		}
//	}
//
//	b.ResetTimer()
//	for i := 0; i < b.N; i++ {
//		// измеряем cost полного replay от середины до конца (пример)
//		start := uint64(perNode / 2)
//		count := 0
//		if err := w.Replay(nodeID, start, func(u Entry) error {
//			// минимальная работа в обработчике — только считать
//			count++
//			return nil
//		}); err != nil {
//			b.Fatalf("Replay: %v", err)
//		}
//		// guard, чтобы оптимизации не убрали тело обработчика
//		if count == 0 {
//			b.Fatalf("unexpected count=0 in replay")
//		}
//	}
//}
