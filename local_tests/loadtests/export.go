package loadtest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// MetricsExport структура для экспорта в JSON
type MetricsExport struct {
	TestName   string  `json:"test_name"`
	Total      uint64  `json:"total"`
	Success    uint64  `json:"success"`
	Failed     uint64  `json:"failed"`
	Duration   float64 `json:"duration_seconds"`
	Throughput float64 `json:"throughput_rps"`

	LatencyMs struct {
		Min    float64 `json:"min"`
		Avg    float64 `json:"avg"`
		StdDev float64 `json:"stddev"`
		P50    float64 `json:"p50"`
		P90    float64 `json:"p90"`
		P95    float64 `json:"p95"`
		P99    float64 `json:"p99"`
		P999   float64 `json:"p999"`
		Max    float64 `json:"max"`
	} `json:"latency_ms"`

	Config struct {
		Concurrency  int    `json:"concurrency"`
		RateLimitRPS int    `json:"rate_limit_rps"`
		PayloadSize  int    `json:"payload_size"`
		TargetAddr   string `json:"target_addr"`
	} `json:"config"`
}

func SaveMetricsJSON(testName string, cfg LoadConfig, m *Metrics, w io.Writer) error {
	export := MetricsExport{
		TestName:   testName,
		Total:      m.Total,
		Success:    m.Success,
		Failed:     m.Failed,
		Duration:   m.Duration().Seconds(),
		Throughput: m.Throughput(),
	}

	export.LatencyMs.Min = float64(m.Min().Microseconds()) / 1000.0
	export.LatencyMs.Avg = float64(m.Avg().Microseconds()) / 1000.0
	export.LatencyMs.StdDev = float64(m.StdDev().Microseconds()) / 1000.0
	export.LatencyMs.P50 = float64(m.Percentile(0.50).Microseconds()) / 1000.0
	export.LatencyMs.P90 = float64(m.Percentile(0.90).Microseconds()) / 1000.0
	export.LatencyMs.P95 = float64(m.Percentile(0.95).Microseconds()) / 1000.0
	export.LatencyMs.P99 = float64(m.Percentile(0.99).Microseconds()) / 1000.0
	export.LatencyMs.P999 = float64(m.Percentile(0.999).Microseconds()) / 1000.0
	export.LatencyMs.Max = float64(m.Max().Microseconds()) / 1000.0

	export.Config.Concurrency = cfg.Concurrency
	export.Config.RateLimitRPS = cfg.RateLimitRPS
	export.Config.PayloadSize = cfg.PayloadSize
	export.Config.TargetAddr = cfg.TargetAddr

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(export)
}

func SaveMetricsCSV(testName string, cfg LoadConfig, m *Metrics, w io.Writer) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	// Header
	header := []string{
		"test_name",
		"total",
		"success",
		"failed",
		"duration_sec",
		"throughput_rps",
		"concurrency",
		"rate_limit_rps",
		"payload_size",
		"latency_min_ms",
		"latency_avg_ms",
		"latency_stddev_ms",
		"latency_p50_ms",
		"latency_p90_ms",
		"latency_p95_ms",
		"latency_p99_ms",
		"latency_p999_ms",
		"latency_max_ms",
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	// Data
	row := []string{
		testName,
		strconv.FormatUint(m.Total, 10),
		strconv.FormatUint(m.Success, 10),
		strconv.FormatUint(m.Failed, 10),
		fmt.Sprintf("%.3f", m.Duration().Seconds()),
		fmt.Sprintf("%.2f", m.Throughput()),
		strconv.Itoa(cfg.Concurrency),
		strconv.Itoa(cfg.RateLimitRPS),
		strconv.Itoa(cfg.PayloadSize),
		fmt.Sprintf("%.3f", float64(m.Min().Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Avg().Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.StdDev().Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Percentile(0.50).Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Percentile(0.90).Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Percentile(0.95).Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Percentile(0.99).Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Percentile(0.999).Microseconds())/1000.0),
		fmt.Sprintf("%.3f", float64(m.Max().Microseconds())/1000.0),
	}

	return writer.Write(row)
}
