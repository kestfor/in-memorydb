package main

import (
	"os"
	"runtime"
	"time"

	"log/slog"

	"github.com/charmbracelet/log"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {

	options := log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
		Formatter:       log.TextFormatter,
	}

	runtime.GOMAXPROCS(3)
	h := log.NewWithOptions(os.Stderr, options)
	slog.SetDefault(slog.New(h))

	cfg := new(Config)
	cfg.loadConfig("./tests/comparison/configs/test-config.yaml")
	reg := prometheus.NewRegistry()
	m := monitoring.NewMetrics(reg)
	monitoring.StartPrometheusServer(cfg.MetricsConfig, reg)

	csvPath := cfg.MetricsConfig.CSVPath
	if csvPath == "" {
		csvPath = "results.csv"
	}
	csvExporter, err := monitoring.NewCSVExporter(reg, csvPath)
	if err != nil {
		slog.Error("failed to create csv exporter", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := csvExporter.Close(); err != nil {
			slog.Warn("csv close error", "err", err)
		}
		slog.Info("CSV results saved", "path", csvPath)
	}()

	for _, db := range cfg.Databases {
		testCfg := cfg.Test
		testCfg.DB = db
		runTest(testCfg, m, csvExporter)
		time.Sleep(1 * time.Minute)
	}

}
