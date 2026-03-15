package main

import (
	"os"
	"time"

	"log/slog"

	"github.com/charmbracelet/log"
	"github.com/kestfor/in-memorydb/local_tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

func main() {

	options := log.Options{
		ReportCaller:    true,
		ReportTimestamp: true,
		TimeFormat:      time.Kitchen,
		Formatter:       log.TextFormatter,
	}

	h := log.NewWithOptions(os.Stderr, options)
	slog.SetDefault(slog.New(h))

	cfg := new(Config)
	cfg.loadConfig("./local_tests/comparison/configs/test-config.yaml")
	reg := prometheus.NewRegistry()
	m := monitoring.NewMetrics(reg)
	monitoring.StartPrometheusServer(cfg.MetricsConfig, reg)

	for _, db := range cfg.Databases {
		testCfg := cfg.Test
		testCfg.DB = db
		runTest(testCfg, m)
		time.Sleep(30 * time.Second)
	}

}
