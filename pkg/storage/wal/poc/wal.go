package wal

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"in-memorydb/pkg/observability/tracing"
	. "in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/types"

	"github.com/vadiminshakov/gowal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

func (ww *walWrapper) Append(ctx context.Context, u *types.Update) error {
	_, span := tracing.StartSpan(ctx, "wal.Append", trace.WithAttributes(attribute.String("node_id", u.NodeID)))
	defer span.End()

	bytes, err := json.Marshal(u)
	if err != nil {
		return tracing.RecordError(ctx, err)
	}

	for i := u.Range.Start; i <= u.Range.End; i++ {
		walIndex := createIndex(u.NodeID, i)
		if err := ww.w.Write(walIndex, u.NodeID, bytes); err != nil {
			return tracing.RecordError(ctx, err)
		}
	}

	return nil
}

func (ww *walWrapper) Get(nodeID string, seq uint64) (*types.Update, error) {
	walIndex := createIndex(nodeID, seq)

	_, val, err := ww.w.Get(walIndex)
	if err != nil {
		return nil, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w: %w", nodeID, seq, err, ErrNotFound)
	}

	var u types.Update
	err = json.Unmarshal(val, &u)
	if err != nil {
		return nil, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w", nodeID, seq, err)
	}

	return &u, nil
}

func (ww *walWrapper) Replay(ctx context.Context, nodeID string, fromSeq uint64, fn func(update *types.Update) error) error {
	_, span := tracing.StartSpan(ctx, "wal.Replay", trace.WithAttributes(attribute.String("node_id", nodeID)))
	defer span.End()

	for msg := range ww.w.Iterator() {
		idx := msg.Idx & 0xffffffff
		key := msg.Key
		val := msg.Value

		if key == nodeID && idx >= fromSeq {
			var u types.Update
			err := json.Unmarshal(val, &u)
			if err != nil {
				return tracing.RecordError(ctx, fmt.Errorf("WAL.Replay(node: '%s', seq: '%d'): %w", nodeID, idx, err))
			}
			if err := fn(&u); err != nil {
				return tracing.RecordError(ctx, fmt.Errorf("WAL.Replay(node: '%s', seq: '%d'): %w", nodeID, idx, err))
			}
		}
	}
	return nil
}

func (ww *walWrapper) ReplayAll(ctx context.Context, fn func(update *types.Update) error) error {
	_, span := tracing.StartSpan(ctx, "wal.ReplayAll")
	defer span.End()

	var u types.Update
	for msg := range ww.w.Iterator() {
		err := json.Unmarshal(msg.Value, &u)
		if err != nil {
			return tracing.RecordError(ctx, fmt.Errorf("WAL.ReplayAll(node: '%s'): %w", msg.Key, err))
		}
		if err := fn(&u); err != nil {
			return tracing.RecordError(ctx, fmt.Errorf("WAL.ReplayAll(node: '%s'): %w", msg.Key, err))
		}
	}
	return nil
}

func (ww *walWrapper) Close() error {
	return ww.w.Close()
}
