package wal

import (
	"hash/crc32"
	. "in-memorydb/pkg/storage/wal"

	"github.com/vadiminshakov/gowal"
)

type walWrapper struct {
	w *gowal.Wal
}

func Open(dir string) (WAL, error) { // TODO use persistence config
	cfg := gowal.Config{
		Dir:              dir,
		Prefix:           "segment_",
		SegmentThreshold: 1000,  // TODO в конфиг
		MaxSegments:      10000, // TODO поставить порог
	}
	w, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, err
	}
	return &walWrapper{
		w: w,
	}, nil
}

// TODO можно кэшировать
func createIndex(nodeID string, seqNum uint64) uint64 {
	h := uint64(crc32.ChecksumIEEE([]byte(nodeID)))
	return (h << 32) | (seqNum & 0xffffffff)
}

func (ww *walWrapper) Append(u *Entry) error {
	walIndex := createIndex(u.NodeID, u.SeqNum)
	key := u.NodeID

	if err := ww.w.Write(walIndex, key, u.Payload); err != nil {
		return err
	}

	return nil
}

func (ww *walWrapper) Get(nodeID string, seq uint64) (*Entry, error) {
	walIndex := createIndex(nodeID, seq)

	k, val, err := ww.w.Get(walIndex)
	if err != nil {
		return nil, err
	}

	return &Entry{
		NodeID:  k,
		SeqNum:  seq,
		Payload: val,
	}, nil
}

func (ww *walWrapper) Replay(nodeID string, fromSeq uint64, fn func(Entry) error) error {
	for msg := range ww.w.Iterator() {
		idx := msg.Idx & 0xffffffff
		key := msg.Key
		val := msg.Value

		if key == nodeID && idx >= fromSeq {
			if err := fn(Entry{
				NodeID:  key,
				SeqNum:  idx,
				Payload: val,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ww *walWrapper) Close() error {
	return ww.w.Close()
}
