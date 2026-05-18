package monitoring

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const histMetricName = "myapp_request_duration_seconds"

// CSVExporter appends one row per (db, op) at the end of each test stage.
// Latency metrics are computed as deltas from the previous stage so that
// each row reflects only the activity during that stage.
type CSVExporter struct {
	mu        sync.Mutex
	reg       *prometheus.Registry
	writer    *csv.Writer
	file      *os.File
	prevState map[string]*histSnapshot // key = "db:op"
}

type histSnapshot struct {
	count   uint64
	sum     float64
	buckets []bucketEntry // sorted by upper bound, cumulative counts
}

type bucketEntry struct {
	upperBound float64
	cumCount   uint64
}

// NewCSVExporter creates the CSV file, writes the header row, and returns the exporter.
func NewCSVExporter(reg *prometheus.Registry, path string) (*CSVExporter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create csv %q: %w", path, err)
	}
	w := csv.NewWriter(f)
	header := []string{
		"timestamp", "db", "op", "clients",
		"stage_duration_s", "requests", "throughput_rps",
		"mean_ms", "p50_ms", "p90_ms", "p95_ms", "p99_ms",
	}
	if err := w.Write(header); err != nil {
		_ = f.Close()
		return nil, err
	}
	w.Flush()
	return &CSVExporter{
		reg:       reg,
		writer:    w,
		file:      f,
		prevState: make(map[string]*histSnapshot),
	}, nil
}

// RecordStage gathers metrics for the given db label, computes per-stage deltas,
// and writes one row per op (set/get) to the CSV file.
// stageDurationS is the wall-clock length of the stage in seconds.
func (e *CSVExporter) RecordStage(db string, clients int, stageDurationS float64) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	mfs, err := e.reg.Gather()
	if err != nil {
		return fmt.Errorf("gather metrics: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	for _, mf := range mfs {
		if mf.GetName() != histMetricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "db") != db {
				continue
			}
			op := labelValue(m, "op")
			key := db + ":" + op

			curr := snapshotFromProto(m.GetHistogram())
			prev := e.prevState[key]
			e.prevState[key] = curr

			row := buildRow(curr, prev, now, db, op, clients, stageDurationS)
			if err := e.writer.Write(row); err != nil {
				return err
			}
		}
	}
	e.writer.Flush()
	return e.writer.Error()
}

// Close flushes and closes the underlying file.
func (e *CSVExporter) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.writer.Flush()
	return e.file.Close()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func labelValue(m *dto.Metric, name string) string {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

func snapshotFromProto(h *dto.Histogram) *histSnapshot {
	s := &histSnapshot{
		count: h.GetSampleCount(),
		sum:   h.GetSampleSum(),
	}
	for _, b := range h.GetBucket() {
		s.buckets = append(s.buckets, bucketEntry{
			upperBound: b.GetUpperBound(),
			cumCount:   b.GetCumulativeCount(),
		})
	}
	sort.Slice(s.buckets, func(i, j int) bool {
		return s.buckets[i].upperBound < s.buckets[j].upperBound
	})
	return s
}

func buildRow(curr, prev *histSnapshot, now, db, op string, clients int, durationS float64) []string {
	var deltaCount uint64
	var deltaSum float64
	var deltaBuckets []bucketEntry

	if prev == nil {
		deltaCount = curr.count
		deltaSum = curr.sum
		deltaBuckets = curr.buckets
	} else {
		deltaCount = curr.count - prev.count
		deltaSum = curr.sum - prev.sum
		for i, b := range curr.buckets {
			var prevCum uint64
			if i < len(prev.buckets) {
				prevCum = prev.buckets[i].cumCount
			}
			deltaBuckets = append(deltaBuckets, bucketEntry{
				upperBound: b.upperBound,
				cumCount:   b.cumCount - prevCum,
			})
		}
	}

	var meanMs, p50, p90, p95, p99 float64
	if deltaCount > 0 {
		meanMs = (deltaSum / float64(deltaCount)) * 1000
		p50 = percentile(deltaBuckets, deltaCount, 0.50) * 1000
		p90 = percentile(deltaBuckets, deltaCount, 0.90) * 1000
		p95 = percentile(deltaBuckets, deltaCount, 0.95) * 1000
		p99 = percentile(deltaBuckets, deltaCount, 0.99) * 1000
	}

	throughput := float64(deltaCount) / durationS

	return []string{
		now,
		db,
		op,
		fmt.Sprintf("%d", clients),
		fmt.Sprintf("%.1f", durationS),
		fmt.Sprintf("%d", deltaCount),
		fmt.Sprintf("%.2f", throughput),
		fmt.Sprintf("%.4f", meanMs),
		fmt.Sprintf("%.4f", p50),
		fmt.Sprintf("%.4f", p90),
		fmt.Sprintf("%.4f", p95),
		fmt.Sprintf("%.4f", p99),
	}
}

// percentile estimates the p-th quantile (0..1) via linear interpolation
// within histogram buckets. deltaBuckets must contain *cumulative* delta counts.
func percentile(buckets []bucketEntry, totalCount uint64, p float64) float64 {
	if len(buckets) == 0 || totalCount == 0 {
		return 0
	}
	target := uint64(math.Ceil(p * float64(totalCount)))

	var prevUB float64
	var prevCum uint64
	for _, b := range buckets {
		if b.cumCount >= target {
			width := b.upperBound - prevUB
			bucketCount := b.cumCount - prevCum
			if bucketCount == 0 || width <= 0 {
				return b.upperBound
			}
			fraction := float64(target-prevCum) / float64(bucketCount)
			return prevUB + fraction*width
		}
		prevUB = b.upperBound
		prevCum = b.cumCount
	}
	return buckets[len(buckets)-1].upperBound
}
