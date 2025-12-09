package loadtest

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	Total     uint64
	Success   uint64
	Failed    uint64
	Latencies []time.Duration
	mu        sync.Mutex
	StartTime time.Time
	EndTime   time.Time

	// Буферизация для уменьшения lock contention
	latencyBuffer chan time.Duration
	done          chan struct{}
	finished      uint32 // Атомарный флаг для защиты от повторного вызова Finish()
}

func NewMetrics() *Metrics {
	m := &Metrics{
		Latencies:     make([]time.Duration, 0, 200000),
		StartTime:     time.Now(),
		latencyBuffer: make(chan time.Duration, 10000), // Буфер для latencies
		done:          make(chan struct{}),
	}

	// Запускаем горутину для сбора latencies
	go m.collectLatencies()

	return m
}

func (m *Metrics) collectLatencies() {
	batch := make([]time.Duration, 0, 1000)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case lat := <-m.latencyBuffer:
			batch = append(batch, lat)
			if len(batch) >= 1000 {
				m.flushBatch(batch)
				batch = make([]time.Duration, 0, 1000)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				m.flushBatch(batch)
				batch = make([]time.Duration, 0, 1000)
			}
		case <-m.done:
			// Финальная очистка буфера
			for len(m.latencyBuffer) > 0 {
				batch = append(batch, <-m.latencyBuffer)
			}
			if len(batch) > 0 {
				m.flushBatch(batch)
			}
			return
		}
	}
}

func (m *Metrics) flushBatch(batch []time.Duration) {
	m.mu.Lock()
	m.Latencies = append(m.Latencies, batch...)
	m.mu.Unlock()
}

func (m *Metrics) Record(lat time.Duration, ok bool) {
	// Неблокирующая отправка latency
	select {
	case m.latencyBuffer <- lat:
	default:
		// Если буфер полон, блокируемся (не теряем данные)
		m.latencyBuffer <- lat
	}

	if ok {
		atomic.AddUint64(&m.Success, 1)
	} else {
		atomic.AddUint64(&m.Failed, 1)
	}
	atomic.AddUint64(&m.Total, 1)
}

func (m *Metrics) Finish() {
	// Используем atomic CAS для защиты от повторного вызова
	if atomic.CompareAndSwapUint32(&m.finished, 0, 1) {
		close(m.done)
		time.Sleep(200 * time.Millisecond) // Даем время на flush
		m.EndTime = time.Now()
	}
}

func (m *Metrics) Duration() time.Duration {
	if m.EndTime.IsZero() {
		return time.Since(m.StartTime)
	}
	return m.EndTime.Sub(m.StartTime)
}

func (m *Metrics) Throughput() float64 {
	d := m.Duration().Seconds()
	if d == 0 {
		return 0
	}
	return float64(atomic.LoadUint64(&m.Total)) / d
}

func (m *Metrics) Percentile(p float64) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

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

func (m *Metrics) Min() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Latencies) == 0 {
		return 0
	}

	minVal := m.Latencies[0]
	for _, lat := range m.Latencies {
		if lat < minVal {
			minVal = lat
		}
	}
	return minVal
}

func (m *Metrics) Max() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Latencies) == 0 {
		return 0
	}

	maxVal := m.Latencies[0]
	for _, lat := range m.Latencies {
		if lat > maxVal {
			maxVal = lat
		}
	}
	return maxVal
}

func (m *Metrics) Avg() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Latencies) == 0 {
		return 0
	}

	var sum time.Duration
	for _, lat := range m.Latencies {
		sum += lat
	}
	return sum / time.Duration(len(m.Latencies))
}

func (m *Metrics) StdDev() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Latencies) == 0 {
		return 0
	}

	// Вычисляем avg без повторного lock (DEADLOCK fix)
	var sum time.Duration
	for _, lat := range m.Latencies {
		sum += lat
	}
	avg := sum / time.Duration(len(m.Latencies))

	var sumSquares float64

	for _, lat := range m.Latencies {
		diff := float64(lat - avg)
		sumSquares += diff * diff
	}

	variance := sumSquares / float64(len(m.Latencies))
	return time.Duration(math.Sqrt(variance))
}

// Snapshot возвращает текущее состояние метрик (для реалтайм вывода)
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Total:      atomic.LoadUint64(&m.Total),
		Success:    atomic.LoadUint64(&m.Success),
		Failed:     atomic.LoadUint64(&m.Failed),
		Duration:   m.Duration(),
		Throughput: m.Throughput(),
	}
}

type MetricsSnapshot struct {
	Total      uint64
	Success    uint64
	Failed     uint64
	Duration   time.Duration
	Throughput float64
}
