package wal

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/kestfor/in-memorydb/pkg/observability/spans"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	. "github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/types"
	"go.uber.org/atomic"

	"github.com/kestfor/gowal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type Config struct {

	// Path specifies the file system location to be used.
	Path string `yaml:"path" env:"WAL_PATH" default:"./wal"`

	// SegmentThreshold is the number of records after which a new segment is created
	SegmentThreshold int `yaml:"segment_threshold" env:"WAL_SEGMENT_THRESHOLD" default:"1000"`

	// MaxSegments is the maximum number of segments allowed before the oldest segment is deleted
	MaxSegmentsNum int `yaml:"-" env:"WAL_MAX_SEGMENTS" default:"100000000"`

	// BatchSize is the number of records after which a batch is flushed to the WAL
	BatchSize int `yaml:"batch_size" default:"1000" env:"WAL_BATCH_SIZE"`

	// FlushInterval is the interval after which a batch is flushed to the WAL
	// 0s means that the batch will be flushed only when the batch is full
	FlushInterval time.Duration `yaml:"flush_interval" default:"0s" env:"WAL_FLUSH_INTERVAL"`

	// SyncMode enables synchronous writes to the WAL,
	// which ensures that the data is written to disk before the write operation returns,
	// by calling fsync on the file descriptor.
	// This is useful for applications that require no data loss in case of a crash.
	SyncMode bool `yaml:"sync_mode" default:"false" env:"WAL_SYNC_MODE"`
}

type walWrapper struct {
	w      *gowal.Wal
	keyGen *KeyGen

	// flushTimestamp is the last time a batch was flushed to the WAL.
	// Used by backgroundFlush to skip ticks when a flush already happened.
	flushTimestamp atomic.Time

	flushInterval time.Duration

	// appendMu guards the active batch slice and batchSize.
	// Held only for slice append + possible swap — never during disk I/O.
	appendMu sync.Mutex

	// flushMu serialises disk writes so that concurrent flush triggers
	// (when two goroutines both fill a batch simultaneously) do not interleave
	// writes to the underlying WAL file.
	flushMu sync.Mutex

	batch     []gowal.Record
	batchSize int
	batchCap  int
}

func New(config Config) (WAL, error) {
	cfg := gowal.Config{
		Dir:              config.Path,
		Prefix:           "segment_",
		SegmentThreshold: config.SegmentThreshold,
		MaxSegments:      config.MaxSegmentsNum,
		IsInSyncDiskMode: config.SyncMode,
	}

	w, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, err
	}
	res := &walWrapper{
		w:              w,
		batchSize:      0,
		batchCap:       config.BatchSize,
		flushInterval:  config.FlushInterval,
		flushTimestamp: *atomic.NewTime(time.Now()),
		batch:          make([]gowal.Record, 0, config.BatchSize),
		keyGen:         NewKeyGen(),
	}

	if config.FlushInterval > 0 {
		go res.backgroundFlush()
	}

	return res, nil
}

// takeBatch atomically swaps the active batch for a fresh one and returns the
// old batch. Returns nil when the batch is empty.
// Must be called with appendMu held.
func (ww *walWrapper) takeBatch() []gowal.Record {
	if ww.batchSize == 0 {
		return nil
	}
	old := ww.batch
	ww.batch = make([]gowal.Record, 0, ww.batchCap)
	ww.batchSize = 0
	return old
}

// writeBatch writes the given batch to disk under flushMu.
func (ww *walWrapper) writeBatch(batch []gowal.Record) error {
	ww.flushMu.Lock()
	err := ww.w.WriteBatch(batch)
	ww.flushMu.Unlock()
	if err == nil {
		ww.flushTimestamp.Store(time.Now())
	}
	return err
}

func (ww *walWrapper) backgroundFlush() {
	slog.Info("running flush WAL in background")
	ticker := time.NewTicker(ww.flushInterval)
	for range ticker.C {
		if time.Since(ww.flushTimestamp.Load()) < ww.flushInterval {
			continue
		}

		ww.appendMu.Lock()
		batch := ww.takeBatch()
		ww.appendMu.Unlock()

		if batch != nil {
			if err := ww.writeBatch(batch); err != nil {
				slog.Error("WAL backgroundFlush: write failed", "err", err)
			} else {
				slog.Debug("WAL backgroundFlush: flushed")
			}
		}
	}
}

func (ww *walWrapper) Append(ctx context.Context, u types.Update) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALAppend, trace.WithAttributes(attribute.String("node_id", u.NodeID)))
	defer span.End()

	_, marshSpan := tracing.StartSpan(ctx, spans.SpanWALAppendMarshal)
	bytes, err := json.Marshal(u)
	if err != nil {
		return tracing.RecordError(ctx, err)
	}
	marshSpan.End()

	_, writeSpan := tracing.StartSpan(ctx, spans.SpanWALAppendWrite)
	defer writeSpan.End()

	record := gowal.Record{
		Key:   u.NodeID,
		Index: ww.keyGen.Key(u.NodeID, u.Seq),
		Value: bytes,
	}

	// Critical section: append to in-memory batch only.
	// If the batch is now full, swap it out atomically and release the lock
	// before touching the disk — other goroutines can keep appending immediately.
	ww.appendMu.Lock()
	ww.batch = append(ww.batch, record)
	ww.batchSize++
	var toFlush []gowal.Record
	if ww.batchSize >= ww.batchCap {
		toFlush = ww.takeBatch()
	}
	ww.appendMu.Unlock()

	if toFlush != nil {
		if err = ww.writeBatch(toFlush); err != nil {
			return tracing.RecordError(ctx, err)
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) Get(nodeID string, seq uint64) (types.Update, error) {
	walIndex := ww.keyGen.Key(nodeID, seq)

	ww.appendMu.Lock()
	for i := 0; i < ww.batchSize; i++ {
		if ww.batch[i].Index == walIndex {
			val := ww.batch[i].Value // copy slice header before unlock
			ww.appendMu.Unlock()
			var u types.Update
			if err := json.Unmarshal(val, &u); err != nil {
				return types.Update{}, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w", nodeID, seq, err)
			}
			return u, nil
		}
	}
	ww.appendMu.Unlock()

	_, val, err := ww.w.Get(walIndex)
	if err != nil {
		return types.Update{}, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w: %w", nodeID, seq, err, ErrNotFound)
	}

	var u types.Update
	if err = json.Unmarshal(val, &u); err != nil {
		return types.Update{}, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w", nodeID, seq, err)
	}

	return u, nil
}

func (ww *walWrapper) Replay(ctx context.Context, nodeID string, fromSeq uint64, fn func(update types.Update) error) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALReplay, trace.WithAttributes(attribute.String("node_id", nodeID)))
	defer span.End()

	// TODO устарело, нужно исправить вычисление idx
	for msg := range ww.w.Iterator() {
		idx := msg.Idx & idMask
		key := msg.Key
		val := msg.Value

		if key == nodeID && idx >= fromSeq {
			var u types.Update
			err := json.Unmarshal(val, &u)
			if err != nil {
				return tracing.RecordError(ctx, fmt.Errorf("WAL.Replay(node: '%s', seq: '%d'): %w", nodeID, idx, err))
			}
			if err := fn(u); err != nil {
				return tracing.RecordError(ctx, fmt.Errorf("WAL.Replay(node: '%s', seq: '%d'): %w", nodeID, idx, err))
			}
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) ReplayAll(ctx context.Context, fn func(update types.Update) error) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALReplayAll)
	defer span.End()

	var u types.Update
	for msg := range ww.w.Iterator() {
		err := json.Unmarshal(msg.Value, &u)
		if err != nil {
			return tracing.RecordError(ctx, fmt.Errorf("WAL.ReplayAll(node: '%s'): %w", msg.Key, err))
		}
		if err := fn(u); err != nil {
			return tracing.RecordError(ctx, fmt.Errorf("WAL.ReplayAll(node: '%s'): %w", msg.Key, err))
		}
	}

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) Close() error {
	ww.appendMu.Lock()
	batch := ww.takeBatch()
	ww.appendMu.Unlock()

	if batch != nil {
		ww.flushMu.Lock()
		err := ww.w.WriteBatch(batch)
		ww.flushMu.Unlock()
		if err != nil {
			return err
		}
	}
	return ww.w.Close()
}
