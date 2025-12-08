package loadtest

import (
	"sort"
	"sync"
	"time"
)

type Metrics struct {
	Total     uint64
	Success   uint64
	Failed    uint64
	Latencies []time.Duration
	mu        sync.Mutex
}

func (m *Metrics) Record(lat time.Duration, ok bool) {
	m.mu.Lock()
	m.Latencies = append(m.Latencies, lat)
	m.mu.Unlock()

	if ok {
		m.Success++
	} else {
		m.Failed++
	}
	m.Total++
}

func (m *Metrics) Percentile(p float64) time.Duration {
	if len(m.Latencies) == 0 {
		return 0
	}

	arr := make([]time.Duration, len(m.Latencies))
	copy(arr, m.Latencies)

	sort.Slice(arr, func(i, j int) bool { return arr[i] < arr[j] })

	idx := int(float64(len(arr)) * p)
	if idx >= len(arr) {
		idx = len(arr) - 1
	}
	return arr[idx]
}
