package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	lt "in-memorydb/local_tests/loadtests"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Значения по умолчанию
const (
	defaultHost           = "127.0.0.1"
	defaultPort           = 50051
	defaultDuration       = 30 * time.Second
	defaultConcurrency    = 50
	defaultRequests       = 0 // 0 = использовать duration
	defaultRateLimitRPS   = 0 // 0 = без ограничения
	defaultPayloadSize    = 256
	defaultCounterStep    = 1
	defaultMixedSetPct    = 20
	defaultMixedGetPct    = 40
	defaultMixedApplyPct  = 30
	defaultMixedDeletePct = 10
)

// Config - конфигурация с поддержкой YAML
type Config struct {
	// Подключение
	Host string `yaml:"host"`
	Port int    `yaml:"port"`

	// Тестирование
	Duration     time.Duration `yaml:"duration"`
	Requests     int           `yaml:"requests"`
	Concurrency  int           `yaml:"concurrency"`
	RateLimitRPS int           `yaml:"rate_limit_rps"`

	// Данные
	PayloadSize int   `yaml:"payload_size"`
	CounterStep int64 `yaml:"counter_step"`

	// Mixed workload
	MixedSetPct    int `yaml:"mixed_set_pct"`
	MixedGetPct    int `yaml:"mixed_get_pct"`
	MixedApplyPct  int `yaml:"mixed_apply_pct"`
	MixedDeletePct int `yaml:"mixed_delete_pct"`

	// Тесты
	Tests []string `yaml:"tests"`

	// Вывод
	OutputDir    string `yaml:"output_dir"`
	JSONOutput   string `yaml:"json_output"`
	CSVOutput    string `yaml:"csv_output"`
	Quiet        bool   `yaml:"quiet"`
	ShowProgress bool   `yaml:"show_progress"`
}

var (
	cfgFile string
	cfg     Config
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lume-bench",
		Short: "Redis-bench style benchmark tool for LUME in-memory database",
		Long: `LUME-BENCH v2.0

Redis-bench style load testing tool for LUME in-memory database.
Supports SET, GET, APPLY, DELETE, and MIXED workloads with configurable parameters.

Examples:
  # Simple test (100k requests, 50 clients)
  lume-bench -c 50 -n 100000

  # Run SET test for 60 seconds
  lume-bench -t set -c 100 -d 60s

  # All tests with duration
  lume-bench -t all -c 50 -d 30s

  # Use config file
  lume-bench --config benchmark.yaml

  # Override config with flags
  lume-bench --config benchmark.yaml -d 2m -c 100

  # Export results
  lume-bench -t all -c 50 -d 30s --json results.json --csv results.csv

  # Rate limited test
  lume-bench -t mixed -c 50 -d 60s --rps 10000`,
		RunE: runBenchmark,
	}

	// Конфиг файл
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Path to config file (YAML)")

	// Подключение
	rootCmd.Flags().StringVar(&cfg.Host, "host", defaultHost, "Server hostname")
	rootCmd.Flags().IntVarP(&cfg.Port, "port", "p", defaultPort, "Server port")

	// Тестирование
	rootCmd.Flags().IntVarP(&cfg.Concurrency, "clients", "c", defaultConcurrency, "Number of parallel connections")
	rootCmd.Flags().IntVarP(&cfg.Requests, "requests", "n", defaultRequests, "Total number of requests (0 = use duration)")
	rootCmd.Flags().DurationVarP(&cfg.Duration, "duration", "d", defaultDuration, "Duration of test (e.g., 60s, 1m)")
	rootCmd.Flags().StringSliceVarP(&cfg.Tests, "test", "t", []string{"all"}, "Test types: set, get, apply, delete, mixed, all")
	rootCmd.Flags().IntVar(&cfg.RateLimitRPS, "rps", defaultRateLimitRPS, "Rate limit (requests per second, 0 = unlimited)")

	// Данные
	rootCmd.Flags().IntVar(&cfg.PayloadSize, "size", defaultPayloadSize, "Payload size in bytes")
	rootCmd.Flags().Int64Var(&cfg.CounterStep, "step", defaultCounterStep, "Counter increment/decrement step")

	// Mixed workload
	rootCmd.Flags().IntVar(&cfg.MixedSetPct, "set-pct", defaultMixedSetPct, "Percentage of SET operations in mixed mode")
	rootCmd.Flags().IntVar(&cfg.MixedGetPct, "get-pct", defaultMixedGetPct, "Percentage of GET operations in mixed mode")
	rootCmd.Flags().IntVar(&cfg.MixedApplyPct, "apply-pct", defaultMixedApplyPct, "Percentage of APPLY operations in mixed mode")
	rootCmd.Flags().IntVar(&cfg.MixedDeletePct, "delete-pct", defaultMixedDeletePct, "Percentage of DELETE operations in mixed mode")

	// Вывод
	rootCmd.Flags().StringVarP(&cfg.OutputDir, "output", "o", ".", "Output directory for results")
	rootCmd.Flags().StringVar(&cfg.CSVOutput, "csv", "", "Export results to CSV file")
	rootCmd.Flags().StringVar(&cfg.JSONOutput, "json", "", "Export results to JSON file")
	rootCmd.Flags().BoolVarP(&cfg.Quiet, "quiet", "q", false, "Quiet mode (only show final results)")
	rootCmd.Flags().BoolVar(&cfg.ShowProgress, "progress", true, "Show real-time progress")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runBenchmark(cmd *cobra.Command, _ []string) error {
	// Загружаем конфиг файл если указан
	if cfgFile != "" {
		if err := loadConfigFile(cfgFile, cmd); err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}

	// Валидация параметров
	if cfg.Requests == 0 && cfg.Duration == 0 {
		cfg.Duration = defaultDuration
	}

	if cfg.Requests > 0 && cfg.Duration > 0 && !cmd.Flags().Changed("duration") {
		// Если указаны requests, сбрасываем duration
		cfg.Duration = 0
	}

	if cfg.Requests > 0 && cfg.Duration > 0 && cmd.Flags().Changed("duration") && cmd.Flags().Changed("requests") {
		return fmt.Errorf("cannot specify both -n (requests) and -d (duration)")
	}

	// Валидация
	if err := validateConfig(); err != nil {
		return err
	}

	// Создаём директорию для вывода если нужно
	if cfg.OutputDir != "." {
		if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	// Вычисляем duration
	testDuration := cfg.Duration
	if cfg.Requests > 0 {
		if cfg.RateLimitRPS > 0 {
			testDuration = time.Duration(float64(cfg.Requests)/float64(cfg.RateLimitRPS)) * time.Second
		} else {
			testDuration = 60 * time.Second
		}
	}

	// Конфигурация нагрузочного теста
	loadCfg := lt.LoadConfig{
		TargetAddr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Duration:       testDuration,
		Concurrency:    cfg.Concurrency,
		RateLimitRPS:   cfg.RateLimitRPS,
		PayloadSize:    cfg.PayloadSize,
		CounterStep:    cfg.CounterStep,
		MixedSetPct:    cfg.MixedSetPct,
		MixedGetPct:    cfg.MixedGetPct,
		MixedApplyPct:  cfg.MixedApplyPct,
		MixedDeletePct: cfg.MixedDeletePct,
	}

	// Определяем какие тесты запускать
	tests := expandTests(cfg.Tests)
	if len(tests) == 0 {
		return fmt.Errorf("no valid tests specified")
	}

	if !cfg.Quiet {
		printHeader(loadCfg, tests)
	}

	// Запускаем тесты
	for i, test := range tests {
		loadCfg.Type = test

		if !cfg.Quiet && i > 0 {
			fmt.Println() // Разделитель между тестами
		}

		m := lt.RunLoadTestWithProgress(loadCfg, cfg.ShowProgress && !cfg.Quiet)

		// Выводим результаты
		if !cfg.Quiet {
			lt.PrintResults(test, loadCfg, m)
		} else {
			fmt.Printf("%s: %.2f req/s (%.3f ms avg latency)\n",
				strings.ToUpper(test),
				m.Throughput(),
				float64(m.Avg().Microseconds())/1000.0)
		}

		// Экспорт результатов
		if cfg.CSVOutput != "" {
			if err := exportToCSV(test, loadCfg, m, i == 0); err != nil {
				log.Printf("Failed to save CSV: %v", err)
			}
		}

		if cfg.JSONOutput != "" {
			if err := exportToJSON(test, loadCfg, m); err != nil {
				log.Printf("Failed to save JSON: %v", err)
			}
		}
	}

	if !cfg.Quiet && len(tests) > 1 {
		fmt.Println()
		fmt.Println("=================================================")
		fmt.Println("         ALL TESTS COMPLETED!")
		fmt.Println("=================================================")
	}

	return nil
}

func loadConfigFile(path string, cmd *cobra.Command) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var fileCfg Config
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return err
	}

	// Применяем значения из файла только если флаги не были явно установлены
	if fileCfg.Host != "" && !cmd.Flags().Changed("host") {
		cfg.Host = fileCfg.Host
	}
	if fileCfg.Port != 0 && !cmd.Flags().Changed("port") {
		cfg.Port = fileCfg.Port
	}
	if fileCfg.Duration != 0 && !cmd.Flags().Changed("duration") {
		cfg.Duration = fileCfg.Duration
	}
	if fileCfg.Requests != 0 && !cmd.Flags().Changed("requests") {
		cfg.Requests = fileCfg.Requests
	}
	if fileCfg.Concurrency != 0 && !cmd.Flags().Changed("clients") {
		cfg.Concurrency = fileCfg.Concurrency
	}
	if fileCfg.RateLimitRPS != 0 && !cmd.Flags().Changed("rps") {
		cfg.RateLimitRPS = fileCfg.RateLimitRPS
	}
	if fileCfg.PayloadSize != 0 && !cmd.Flags().Changed("size") {
		cfg.PayloadSize = fileCfg.PayloadSize
	}
	if fileCfg.CounterStep != 0 && !cmd.Flags().Changed("step") {
		cfg.CounterStep = fileCfg.CounterStep
	}
	if fileCfg.MixedSetPct != 0 && !cmd.Flags().Changed("set-pct") {
		cfg.MixedSetPct = fileCfg.MixedSetPct
	}
	if fileCfg.MixedGetPct != 0 && !cmd.Flags().Changed("get-pct") {
		cfg.MixedGetPct = fileCfg.MixedGetPct
	}
	if fileCfg.MixedApplyPct != 0 && !cmd.Flags().Changed("apply-pct") {
		cfg.MixedApplyPct = fileCfg.MixedApplyPct
	}
	if fileCfg.MixedDeletePct != 0 && !cmd.Flags().Changed("delete-pct") {
		cfg.MixedDeletePct = fileCfg.MixedDeletePct
	}
	if len(fileCfg.Tests) > 0 && !cmd.Flags().Changed("test") {
		cfg.Tests = fileCfg.Tests
	}
	if fileCfg.OutputDir != "" && !cmd.Flags().Changed("output") {
		cfg.OutputDir = fileCfg.OutputDir
	}
	if fileCfg.JSONOutput != "" && !cmd.Flags().Changed("json") {
		cfg.JSONOutput = fileCfg.JSONOutput
	}
	if fileCfg.CSVOutput != "" && !cmd.Flags().Changed("csv") {
		cfg.CSVOutput = fileCfg.CSVOutput
	}
	if fileCfg.Quiet && !cmd.Flags().Changed("quiet") {
		cfg.Quiet = fileCfg.Quiet
	}
	if !fileCfg.ShowProgress && !cmd.Flags().Changed("progress") {
		cfg.ShowProgress = fileCfg.ShowProgress
	}

	return nil
}

func validateConfig() error {
	if cfg.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be positive")
	}
	if cfg.Duration < 0 {
		return fmt.Errorf("duration cannot be negative")
	}
	if cfg.Requests < 0 {
		return fmt.Errorf("requests cannot be negative")
	}
	if cfg.PayloadSize < 0 {
		return fmt.Errorf("payload size cannot be negative")
	}
	if cfg.RateLimitRPS < 0 {
		return fmt.Errorf("rate limit cannot be negative")
	}

	// Валидация mixed процентов
	total := cfg.MixedSetPct + cfg.MixedGetPct + cfg.MixedApplyPct + cfg.MixedDeletePct
	if total != 100 {
		return fmt.Errorf("mixed workload percentages must sum to 100, got %d", total)
	}

	return nil
}

func expandTests(tests []string) []string {
	var result []string
	seen := make(map[string]bool)

	for _, t := range tests {
		normalized := normalizeTestType(t)
		if normalized == "all" {
			// Добавляем все тесты
			for _, test := range []string{lt.TestSet, lt.TestGet, lt.TestApply, lt.TestMixed} {
				if !seen[test] {
					result = append(result, test)
					seen[test] = true
				}
			}
		} else if normalized == "delete" {
			// Delete тестируется в mixed режиме
			if !cfg.Quiet {
				fmt.Println("Note: DELETE is tested as part of mixed workload")
			}
			if !seen[lt.TestMixed] {
				result = append(result, lt.TestMixed)
				seen[lt.TestMixed] = true
			}
		} else if normalized != "" {
			if !seen[normalized] {
				result = append(result, normalized)
				seen[normalized] = true
			}
		} else {
			log.Printf("Warning: unknown test type '%s', skipping", t)
		}
	}

	return result
}

func normalizeTestType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "set":
		return lt.TestSet
	case "get":
		return lt.TestGet
	case "apply":
		return lt.TestApply
	case "mixed":
		return lt.TestMixed
	case "delete":
		return "delete"
	case "all":
		return "all"
	default:
		return ""
	}
}

func printHeader(loadCfg lt.LoadConfig, tests []string) {
	fmt.Println()
	fmt.Println("=================================================")
	fmt.Println("         LUME-BENCH v2.0")
	fmt.Println("         Redis-bench style benchmark tool")
	fmt.Println("=================================================")
	fmt.Printf("Target: %s\n", loadCfg.TargetAddr)
	if cfg.Requests > 0 {
		fmt.Printf("Total requests: %d\n", cfg.Requests)
	}
	if cfg.Duration > 0 {
		fmt.Printf("Duration: %v per test\n", cfg.Duration)
	}
	fmt.Printf("Parallel clients: %d\n", loadCfg.Concurrency)
	if loadCfg.RateLimitRPS > 0 {
		fmt.Printf("Rate limit: %d req/s\n", loadCfg.RateLimitRPS)
	} else {
		fmt.Println("Rate limit: unlimited")
	}
	fmt.Printf("Payload: %d bytes\n", loadCfg.PayloadSize)
	fmt.Printf("Tests: %s\n", strings.Join(tests, ", "))
	fmt.Println("=================================================")
	fmt.Println()
}

func exportToCSV(testName string, loadCfg lt.LoadConfig, m *lt.Metrics, writeHeader bool) (err error) {
	filename := cfg.CSVOutput
	if cfg.OutputDir != "." {
		filename = fmt.Sprintf("%s/%s", cfg.OutputDir, cfg.CSVOutput)
	}

	var f *os.File

	if writeHeader {
		f, err = os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	} else {
		f, err = os.OpenFile(filename, os.O_WRONLY|os.O_APPEND, 0644)
	}

	if err != nil {
		return fmt.Errorf("failed to open CSV file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	return lt.SaveMetricsCSV(testName, loadCfg, m, f)
}

func exportToJSON(testName string, loadCfg lt.LoadConfig, m *lt.Metrics) (err error) {
	filename := cfg.JSONOutput
	if cfg.OutputDir != "." {
		filename = fmt.Sprintf("%s/%s", cfg.OutputDir, cfg.JSONOutput)
	}

	// Добавляем имя теста к файлу если несколько тестов
	if len(cfg.Tests) > 1 || (len(cfg.Tests) == 1 && strings.ToLower(cfg.Tests[0]) == "all") {
		ext := ".json"
		base := strings.TrimSuffix(filename, ext)
		filename = fmt.Sprintf("%s_%s%s", base, testName, ext)
	}

	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open JSON file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	return lt.SaveMetricsJSON(testName, loadCfg, m, f)
}
