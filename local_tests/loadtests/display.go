package loadtest

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// PrintResults выводит результаты в стиле redis-bench
func PrintResults(testName string, cfg LoadConfig, m *Metrics) {
	// Finish() уже вызван в RunLoadTestWithProgress

	total := atomic.LoadUint64(&m.Total)
	success := atomic.LoadUint64(&m.Success)
	failed := atomic.LoadUint64(&m.Failed)
	duration := m.Duration().Seconds()

	fmt.Printf("\n====== %s ======\n", strings.ToUpper(testName))
	fmt.Printf("  %d requests completed in %.2f seconds\n", total, duration)
	fmt.Printf("  %d parallel clients\n", cfg.Concurrency)
	if cfg.PayloadSize > 0 {
		fmt.Printf("  %d bytes payload\n", cfg.PayloadSize)
	}
	fmt.Printf("  keep alive: 1\n\n")

	// Latency distribution
	if len(m.Latencies) > 0 {
		printLatencyDistribution(m)
	}

	// Summary
	fmt.Println("\nSummary:")
	fmt.Printf("  Total requests: %d\n", total)
	fmt.Printf("  Successful: %d (%.2f%%)\n", success, float64(success)/float64(total)*100)
	if failed > 0 {
		fmt.Printf("  Failed: %d (%.2f%%)\n", failed, float64(failed)/float64(total)*100)
	}
	fmt.Printf("  Throughput: %.2f requests/sec\n", m.Throughput())

	if cfg.PayloadSize > 0 {
		bandwidth := m.Throughput() * float64(cfg.PayloadSize) / 1024 / 1024
		fmt.Printf("  Bandwidth: %.2f MB/sec\n", bandwidth)
	}

	// Latency stats
	if len(m.Latencies) > 0 {
		fmt.Println("\nLatency percentiles:")
		fmt.Printf("  Min: %.3f ms\n", float64(m.Min().Microseconds())/1000.0)
		fmt.Printf("  Avg: %.3f ms\n", float64(m.Avg().Microseconds())/1000.0)
		fmt.Printf("  StdDev: %.3f ms\n", float64(m.StdDev().Microseconds())/1000.0)
		fmt.Printf("  p50: %.3f ms\n", float64(m.Percentile(0.50).Microseconds())/1000.0)
		fmt.Printf("  p90: %.3f ms\n", float64(m.Percentile(0.90).Microseconds())/1000.0)
		fmt.Printf("  p95: %.3f ms\n", float64(m.Percentile(0.95).Microseconds())/1000.0)
		fmt.Printf("  p99: %.3f ms\n", float64(m.Percentile(0.99).Microseconds())/1000.0)
		fmt.Printf("  p999: %.3f ms\n", float64(m.Percentile(0.999).Microseconds())/1000.0)
		fmt.Printf("  Max: %.3f ms\n", float64(m.Max().Microseconds())/1000.0)
	}

	fmt.Println()
}

// printLatencyDistribution выводит распределение латентности в стиле redis-bench
func printLatencyDistribution(m *Metrics) {
	buckets := []time.Duration{
		100 * time.Microsecond,
		200 * time.Microsecond,
		500 * time.Microsecond,
		1 * time.Millisecond,
		2 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
		20 * time.Millisecond,
		50 * time.Millisecond,
		100 * time.Millisecond,
		200 * time.Millisecond,
		500 * time.Millisecond,
		1 * time.Second,
	}

	m.mu.Lock()
	total := float64(len(m.Latencies))
	counts := make([]int, len(buckets))

	for _, lat := range m.Latencies {
		for i, bucket := range buckets {
			if lat <= bucket {
				counts[i]++
				break
			}
		}
	}
	m.mu.Unlock()

	fmt.Println("Latency distribution:")
	cumulative := 0
	for i, bucket := range buckets {
		cumulative += counts[i]
		pct := float64(cumulative) / total * 100
		if pct > 0 {
			fmt.Printf("  %.2f%% <= %v\n", pct, formatDuration(bucket))
		}
		if pct >= 100 {
			break
		}
	}
}

// formatDuration форматирует duration для удобного отображения
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.0f microseconds", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.0f milliseconds", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.1f seconds", d.Seconds())
}

// PrintProgress выводит прогресс в реалтайм режиме
func PrintProgress(snapshot MetricsSnapshot) {
	rps := snapshot.Throughput
	elapsed := snapshot.Duration.Seconds()

	fmt.Printf("\r[%.1fs] %d requests | %.0f req/s | success: %d | failed: %d",
		elapsed, snapshot.Total, rps, snapshot.Success, snapshot.Failed)
}

// ClearLine очищает текущую строку в терминале
func ClearLine() {
	fmt.Print("\r" + strings.Repeat(" ", 80) + "\r")
}
