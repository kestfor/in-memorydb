package wal

import (
	"context"
	"fmt"
	"hash/crc32"
	"sync"

	jsoniter "github.com/json-iterator/go"
	"github.com/kestfor/in-memorydb/pkg/observability/spans"
	"github.com/kestfor/in-memorydb/pkg/observability/tracing"
	. "github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/types"

	"github.com/kestfor/gowal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultWalPath          = "./wal"
	defaultSegmentThreshold = 1000
	defaultMaxSegments      = 100000000
	defaultBatchSize        = 500
	defaultWriteChanSize    = 50000
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type Config struct {

	// Path specifies the file system location to be used.
	Path string `yaml:"path" env:"WAL_PATH" default:"./wal"`

	// SegmentThreshold is the number of records after which a new segment is created
	SegmentThreshold int `yaml:"segment_threshold" env:"WAL_SEGMENT_THRESHOLD" default:"1000"`

	// MaxSegments is the maximum number of segments allowed before the oldest segment is deleted
	MaxSegmentsNum int `yaml:"-" default:"100000000"` // TODO add yaml tag after snapshot implementation

	BatchSize int `yaml:"batch_size" default:"500"`

	WriteChanSize int `yaml:"write_chan_size" default:"50000"`
}

type walWrapper struct {
	w *gowal.Wal

	mu        sync.Mutex
	batch     []gowal.Record
	batchSize int
	batchCap  int
}

func (c *Config) populateMissed() {
	if c.Path == "" {
		c.Path = defaultWalPath
	}
	if c.SegmentThreshold == 0 {
		c.SegmentThreshold = defaultSegmentThreshold
	}
	if c.MaxSegmentsNum == 0 {
		c.MaxSegmentsNum = defaultMaxSegments
	}
	if c.BatchSize == 0 {
		c.BatchSize = defaultBatchSize
	}
	if c.WriteChanSize == 0 {
		c.WriteChanSize = defaultWriteChanSize
	}
}

func New(config Config) (WAL, error) {
	config.populateMissed()

	cfg := gowal.Config{
		Dir:              config.Path,
		Prefix:           "segment_",
		SegmentThreshold: config.SegmentThreshold,
		MaxSegments:      config.MaxSegmentsNum,
	}

	w, err := gowal.NewWAL(cfg)
	if err != nil {
		return nil, err
	}
	return &walWrapper{
		w:         w,
		batchSize: 0,
		batchCap:  config.BatchSize,
		batch:     make([]gowal.Record, 0, config.BatchSize),
	}, nil
}

// TODO можно кэшировать
func createIndex(nodeID string, seqNum uint64) uint64 {
	h := uint64(crc32.ChecksumIEEE([]byte(nodeID)))
	return (h << 32) | (seqNum & 0xffffffff)
}

func (ww *walWrapper) Append(ctx context.Context, u *types.Update) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALAppend, trace.WithAttributes(attribute.String("node_id", u.NodeID)))
	defer span.End()

	_, marshSpan := tracing.StartSpan(ctx, spans.SpanWALAppendMarshal)
	bytes, err := json.Marshal(u)
	if err != nil {
		return tracing.RecordError(ctx, err)
	}
	marshSpan.End()

	_, writeSpan := tracing.StartSpan(ctx, spans.SpanWALAppendWrite, trace.WithAttributes(attribute.Int("write_count", int(u.Range.End-u.Range.Start+1))))
	ww.mu.Lock()
	for i := u.Range.Start; i <= u.Range.End; i++ {
		walIndex := createIndex(u.NodeID, i)
		record := gowal.Record{
			Key:   u.NodeID,
			Index: walIndex,
			Value: bytes,
		}

		ww.batch = append(ww.batch, record)
		ww.batchSize++

		if ww.batchSize == ww.batchCap {
			if err := ww.w.WriteBatch(ww.batch); err != nil {
				ww.mu.Unlock()
				return tracing.RecordError(ctx, err)
			}
			ww.batchSize = 0
			ww.batch = ww.batch[:0]
		}

	}
	ww.mu.Unlock()
	writeSpan.End()

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) Get(nodeID string, seq uint64) (*types.Update, error) {
	walIndex := createIndex(nodeID, seq)

	ww.mu.Lock()
	for ind := 0; ind <= ww.batchSize; ind++ {
		if ww.batch[ind].Index == walIndex {
			defer ww.mu.Unlock()
			var u types.Update
			err := json.Unmarshal(ww.batch[ind].Value, &u)
			if err != nil {
				return nil, fmt.Errorf("WAL.Get(node: '%s', seq: '%d'): %w", nodeID, seq, err)
			}
			return &u, nil
		}
	}
	ww.mu.Unlock()

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
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALReplay, trace.WithAttributes(attribute.String("node_id", nodeID)))
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

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) ReplayAll(ctx context.Context, fn func(update *types.Update) error) error {
	ctx, span := tracing.StartSpan(ctx, spans.SpanWALReplayAll)
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

	span.SetStatus(codes.Ok, "")
	return nil
}

func (ww *walWrapper) Close() error {
	if ww.batchSize > 0 {
		if err := ww.w.WriteBatch(ww.batch); err != nil {
			return err
		}
	}
	return ww.w.Close()
}
