package main

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "in-memorydb/api/lumepb" // Замените на ваш путь
)

// Конфигурация теста
type Config struct {
	ServerAddr     string
	Duration       time.Duration
	Workers        int
	KeysCount      int
	ReadRatio      float64 // 0.0-1.0, остальное - записи
	CounterRatio   float64 // 0.0-1.0 от операций записи
	PreWarmup      bool
	WarmupKeys     int
	ReportInterval time.Duration
}

// Метрики с минимальным overhead
type Metrics struct {
	setOps    atomic.Uint64
	getOps    atomic.Uint64
	applyOps  atomic.Uint64
	deleteOps atomic.Uint64
	errors    atomic.Uint64

	// Латентность в наносекундах (для расчёта percentiles)
	latencies chan int64

	startTime time.Time
	endTime   atomic.Value // time.Time

	// История для визуализации
	history    []MetricsSnapshot
	historyMux sync.Mutex
}

// Снимок метрик для истории
type MetricsSnapshot struct {
	Timestamp  time.Time
	SetOps     uint64
	GetOps     uint64
	ApplyOps   uint64
	DeleteOps  uint64
	Errors     uint64
	SetRPS     float64
	GetRPS     float64
	ApplyRPS   float64
	DeleteRPS  float64
	TotalRPS   float64
	LatencyP50 int64
	LatencyP95 int64
	LatencyP99 int64
	LatencyMax int64
}

func NewMetrics(bufferSize int) *Metrics {
	m := &Metrics{
		latencies: make(chan int64, bufferSize),
		startTime: time.Now(),
		history:   make([]MetricsSnapshot, 0, 1000),
	}
	return m
}

// Воркер для выполнения операций
type Worker struct {
	id          int
	client      pb.LumeClient
	config      *Config
	keys        []string
	rng         []byte
	keyTypes    map[string]pb.Type // Отслеживание типов ключей
	keyTypesMux sync.RWMutex
}

func NewWorker(id int, client pb.LumeClient, config *Config, keys []string) *Worker {
	return &Worker{
		id:       id,
		client:   client,
		config:   config,
		keys:     keys,
		rng:      make([]byte, 16),
		keyTypes: make(map[string]pb.Type),
	}
}

// Генерация случайных данных без аллокаций
func (w *Worker) randomBytes(n int) []byte {
	if len(w.rng) < n {
		w.rng = make([]byte, n)
	}
	rand.Read(w.rng[:n])
	return w.rng[:n]
}

// Основной цикл воркера
func (w *Worker) Run(ctx context.Context, metrics *Metrics, wg *sync.WaitGroup) {
	defer wg.Done()

	keyIdx := 0
	opCounter := uint64(0)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			key := w.keys[keyIdx%len(w.keys)]
			keyIdx++

			// Определяем тип операции
			shouldRead := (opCounter % 100) < uint64(w.config.ReadRatio*100)
			opCounter++

			start := time.Now()
			var err error

			if shouldRead {
				err = w.performGet(ctx, key)
				if err == nil {
					metrics.getOps.Add(1)
				}
			} else {
				// Записи: Set + Apply или Delete
				if opCounter%20 == 0 { // 5% delete операций
					err = w.performDelete(ctx, key)
					if err == nil {
						metrics.deleteOps.Add(1)
					}
				} else {
					isCounter := (opCounter % 100) < uint64(w.config.CounterRatio*100)

					// Проверяем существующий тип ключа
					existingType := w.getKeyType(key)

					if existingType == pb.Type_TYPE_NOT_SPECIFIED {
						// Ключ не существует, создаём с нужным типом
						err = w.performSet(ctx, key, isCounter)
						if err == nil {
							metrics.setOps.Add(1)
							// Сохраняем тип
							if isCounter {
								w.setKeyType(key, pb.Type_TYPE_PN_COUNTER)
							} else {
								w.setKeyType(key, pb.Type_TYPE_LWW_REGISTER)
							}
						}
					} else {
						// Ключ существует, используем его тип
						isCounter = (existingType == pb.Type_TYPE_PN_COUNTER)
					}

					// Затем Apply с правильным типом операции
					if err == nil {
						err = w.performApply(ctx, key, isCounter)
						if err == nil {
							metrics.applyOps.Add(1)
						}
					}
				}
			}

			elapsed := time.Since(start).Nanoseconds()

			// Неблокирующая отправка латентности
			select {
			case metrics.latencies <- elapsed:
			default:
				// Буфер переполнен, пропускаем
			}

			if err != nil {
				metrics.errors.Add(1)
			}
		}
	}
}

func (w *Worker) performSet(ctx context.Context, key string, isCounter bool) error {
	crdtType := pb.Type_TYPE_LWW_REGISTER
	if isCounter {
		crdtType = pb.Type_TYPE_PN_COUNTER
	}

	_, err := w.client.Set(ctx, &pb.SetRequest{
		Key:      key,
		CrdtType: crdtType,
	})
	return err
}

func (w *Worker) performGet(ctx context.Context, key string) error {
	_, err := w.client.Get(ctx, &pb.GetRequest{
		Key: key,
	})
	return err
}

func (w *Worker) performApply(ctx context.Context, key string, isCounter bool) error {
	req := &pb.ApplyRequest{
		Key: key,
	}

	if isCounter {
		// 50/50 increment/decrement
		if time.Now().UnixNano()%2 == 0 {
			req.Operation = &pb.ApplyRequest_CounterOperationInc{
				CounterOperationInc: &pb.ApplyRequest_CounterInc{Val: 1},
			}
		} else {
			req.Operation = &pb.ApplyRequest_CounterOperationDec{
				CounterOperationDec: &pb.ApplyRequest_CounterDec{Val: 1},
			}
		}
	} else {
		req.Operation = &pb.ApplyRequest_RegisterOperation{
			RegisterOperation: &pb.ApplyRequest_Register{
				Value: w.randomBytes(64),
			},
		}
	}

	_, err := w.client.Apply(ctx, req)
	return err
}

func (w *Worker) performDelete(ctx context.Context, key string) error {
	_, err := w.client.Delete(ctx, &pb.DeleteRequest{
		Key: key,
	})
	if err == nil {
		// Удаляем тип из кеша
		w.removeKeyType(key)
	}
	return err
}

// Методы для работы с типами ключей
func (w *Worker) getKeyType(key string) pb.Type {
	w.keyTypesMux.RLock()
	defer w.keyTypesMux.RUnlock()
	t, ok := w.keyTypes[key]
	if !ok {
		return pb.Type_TYPE_NOT_SPECIFIED
	}
	return t
}

func (w *Worker) setKeyType(key string, t pb.Type) {
	w.keyTypesMux.Lock()
	defer w.keyTypesMux.Unlock()
	w.keyTypes[key] = t
}

func (w *Worker) removeKeyType(key string) {
	w.keyTypesMux.Lock()
	defer w.keyTypesMux.Unlock()
	delete(w.keyTypes, key)
}

// Прогрев хранилища
func warmup(client pb.LumeClient, keys []string, counterRatio float64, workers []*Worker) {
	ctx := context.Background()
	log.Printf("Прогрев: создание %d ключей...", len(keys))

	for i, key := range keys {
		isCounter := (i % 100) < int(counterRatio*100)
		crdtType := pb.Type_TYPE_LWW_REGISTER
		if isCounter {
			crdtType = pb.Type_TYPE_PN_COUNTER
		}

		_, err := client.Set(ctx, &pb.SetRequest{
			Key:      key,
			CrdtType: crdtType,
		})

		if err == nil {
			// Сохраняем типы во всех воркерах
			for _, w := range workers {
				w.setKeyType(key, crdtType)
			}
		}

		if i%1000 == 0 && i > 0 {
			log.Printf("  создано %d/%d ключей", i, len(keys))
		}
	}
	log.Println("Прогрев завершён")
}

// Сборщик и анализатор метрик
func metricsCollector(metrics *Metrics, interval time.Duration, done <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	latencyBuffer := make([]int64, 0, 100000)
	lastReport := time.Now()

	var lastSet, lastGet, lastApply, lastDelete uint64

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			// Собираем латентности
			latencyBuffer = latencyBuffer[:0]
		drainLoop:
			for {
				select {
				case lat := <-metrics.latencies:
					latencyBuffer = append(latencyBuffer, lat)
				default:
					break drainLoop
				}
			}

			// Текущие счётчики
			sets := metrics.setOps.Load()
			gets := metrics.getOps.Load()
			applies := metrics.applyOps.Load()
			deletes := metrics.deleteOps.Load()
			errors := metrics.errors.Load()

			// Дельта за интервал
			deltaSet := sets - lastSet
			deltaGet := gets - lastGet
			deltaApply := applies - lastApply
			deltaDelete := deletes - lastDelete

			elapsed := time.Since(lastReport).Seconds()

			// RPS
			setRPS := float64(deltaSet) / elapsed
			getRPS := float64(deltaGet) / elapsed
			applyRPS := float64(deltaApply) / elapsed
			deleteRPS := float64(deltaDelete) / elapsed
			totalRPS := setRPS + getRPS + applyRPS + deleteRPS

			// Percentiles латентности
			p50, p95, p99, pMax := calculatePercentiles(latencyBuffer)

			// Сохраняем снимок в историю
			snapshot := MetricsSnapshot{
				Timestamp:  time.Now(),
				SetOps:     sets,
				GetOps:     gets,
				ApplyOps:   applies,
				DeleteOps:  deletes,
				Errors:     errors,
				SetRPS:     setRPS,
				GetRPS:     getRPS,
				ApplyRPS:   applyRPS,
				DeleteRPS:  deleteRPS,
				TotalRPS:   totalRPS,
				LatencyP50: p50,
				LatencyP95: p95,
				LatencyP99: p99,
				LatencyMax: pMax,
			}

			metrics.historyMux.Lock()
			metrics.history = append(metrics.history, snapshot)
			metrics.historyMux.Unlock()

			log.Printf("\n=== Метрики за %.1fs ===", elapsed)
			log.Printf("RPS: Set=%.0f Get=%.0f Apply=%.0f Delete=%.0f Total=%.0f",
				setRPS, getRPS, applyRPS, deleteRPS, totalRPS)
			log.Printf("Операции: Set=%d Get=%d Apply=%d Delete=%d Errors=%d",
				sets, gets, applies, deletes, errors)
			log.Printf("Латентность (μs): p50=%.0f p95=%.0f p99=%.0f max=%.0f",
				float64(p50)/1000, float64(p95)/1000, float64(p99)/1000, float64(pMax)/1000)

			lastSet = sets
			lastGet = gets
			lastApply = applies
			lastDelete = deletes
			lastReport = time.Now()
		}
	}
}

func calculatePercentiles(latencies []int64) (p50, p95, p99, pMax int64) {
	if len(latencies) == 0 {
		return 0, 0, 0, 0
	}

	// Простая сортировка для percentiles
	sorted := make([]int64, len(latencies))
	copy(sorted, latencies)

	// Insertion sort (достаточно быстро для наших целей)
	for i := 1; i < len(sorted); i++ {
		key := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > key {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = key
	}

	p50 = sorted[len(sorted)*50/100]
	p95 = sorted[len(sorted)*95/100]
	p99 = sorted[len(sorted)*99/100]
	pMax = sorted[len(sorted)-1]

	return
}

// Финальный отчёт
func printFinalReport(metrics *Metrics) {
	metrics.endTime.Store(time.Now())
	duration := metrics.endTime.Load().(time.Time).Sub(metrics.startTime).Seconds()

	sets := metrics.setOps.Load()
	gets := metrics.getOps.Load()
	applies := metrics.applyOps.Load()
	deletes := metrics.deleteOps.Load()
	errors := metrics.errors.Load()
	total := sets + gets + applies + deletes

	log.Println("\n" + "============================================================")
	log.Println("ИТОГОВЫЙ ОТЧЁТ")
	log.Println("===============================================================")
	log.Printf("Длительность: %.2f сек", duration)
	log.Printf("Всего операций: %d", total)
	log.Printf("  Set:    %d (%.1f%%)", sets, float64(sets)/float64(total)*100)
	log.Printf("  Get:    %d (%.1f%%)", gets, float64(gets)/float64(total)*100)
	log.Printf("  Apply:  %d (%.1f%%)", applies, float64(applies)/float64(total)*100)
	log.Printf("  Delete: %d (%.1f%%)", deletes, float64(deletes)/float64(total)*100)
	log.Printf("Ошибки: %d (%.2f%%)", errors, float64(errors)/float64(total)*100)
	log.Printf("Средний RPS: %.0f ops/sec", float64(total)/duration)
	log.Println("===================================================================")
}

// Сохранение отчётов
func saveReports(metrics *Metrics, config *Config, outputDir string) error {
	// Создаём директорию для отчётов
	timestamp := time.Now().Format("20060102_150405")
	reportDir := fmt.Sprintf("%s/report_%s", outputDir, timestamp)
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return fmt.Errorf("не удалось создать директорию: %v", err)
	}

	// 1. Сохраняем CSV с детальными метриками
	if err := saveCSVReport(metrics, reportDir); err != nil {
		return fmt.Errorf("ошибка сохранения CSV: %v", err)
	}

	// 2. Сохраняем JSON с конфигурацией и результатами
	if err := saveJSONReport(metrics, config, reportDir); err != nil {
		return fmt.Errorf("ошибка сохранения JSON: %v", err)
	}

	// 3. Генерируем HTML с графиками
	if err := saveHTMLReport(metrics, config, reportDir); err != nil {
		return fmt.Errorf("ошибка сохранения HTML: %v", err)
	}

	log.Printf("\n📊 Отчёты сохранены в: %s", reportDir)
	log.Printf("  - metrics.csv (детальные метрики)")
	log.Printf("  - report.json (конфигурация и результаты)")
	log.Printf("  - report.html (визуализация)")

	return nil
}

// Сохранение CSV
func saveCSVReport(metrics *Metrics, dir string) error {
	file, err := os.Create(fmt.Sprintf("%s/metrics.csv", dir))
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Заголовок
	writer.Write([]string{
		"timestamp", "elapsed_sec",
		"set_ops", "get_ops", "apply_ops", "delete_ops", "errors",
		"set_rps", "get_rps", "apply_rps", "delete_rps", "total_rps",
		"latency_p50_us", "latency_p95_us", "latency_p99_us", "latency_max_us",
	})

	metrics.historyMux.Lock()
	defer metrics.historyMux.Unlock()

	for _, snap := range metrics.history {
		elapsed := snap.Timestamp.Sub(metrics.startTime).Seconds()
		writer.Write([]string{
			snap.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%.2f", elapsed),
			fmt.Sprintf("%d", snap.SetOps),
			fmt.Sprintf("%d", snap.GetOps),
			fmt.Sprintf("%d", snap.ApplyOps),
			fmt.Sprintf("%d", snap.DeleteOps),
			fmt.Sprintf("%d", snap.Errors),
			fmt.Sprintf("%.2f", snap.SetRPS),
			fmt.Sprintf("%.2f", snap.GetRPS),
			fmt.Sprintf("%.2f", snap.ApplyRPS),
			fmt.Sprintf("%.2f", snap.DeleteRPS),
			fmt.Sprintf("%.2f", snap.TotalRPS),
			fmt.Sprintf("%.2f", float64(snap.LatencyP50)/1000),
			fmt.Sprintf("%.2f", float64(snap.LatencyP95)/1000),
			fmt.Sprintf("%.2f", float64(snap.LatencyP99)/1000),
			fmt.Sprintf("%.2f", float64(snap.LatencyMax)/1000),
		})
	}

	return nil
}

// Сохранение JSON
func saveJSONReport(metrics *Metrics, config *Config, dir string) error {
	duration := metrics.endTime.Load().(time.Time).Sub(metrics.startTime).Seconds()

	report := map[string]interface{}{
		"config": map[string]interface{}{
			"server_addr":     config.ServerAddr,
			"duration":        config.Duration.String(),
			"workers":         config.Workers,
			"keys_count":      config.KeysCount,
			"read_ratio":      config.ReadRatio,
			"counter_ratio":   config.CounterRatio,
			"pre_warmup":      config.PreWarmup,
			"warmup_keys":     config.WarmupKeys,
			"report_interval": config.ReportInterval.String(),
		},
		"summary": map[string]interface{}{
			"duration_sec": duration,
			"total_ops":    metrics.setOps.Load() + metrics.getOps.Load() + metrics.applyOps.Load() + metrics.deleteOps.Load(),
			"set_ops":      metrics.setOps.Load(),
			"get_ops":      metrics.getOps.Load(),
			"apply_ops":    metrics.applyOps.Load(),
			"delete_ops":   metrics.deleteOps.Load(),
			"errors":       metrics.errors.Load(),
			"avg_rps":      float64(metrics.setOps.Load()+metrics.getOps.Load()+metrics.applyOps.Load()+metrics.deleteOps.Load()) / duration,
		},
		"snapshots": metrics.history,
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(fmt.Sprintf("%s/report.json", dir), data, 0644)
}

// Генерация HTML с графиками
func saveHTMLReport(metrics *Metrics, config *Config, dir string) error {
	duration := metrics.endTime.Load().(time.Time).Sub(metrics.startTime).Seconds()
	total := metrics.setOps.Load() + metrics.getOps.Load() + metrics.applyOps.Load() + metrics.deleteOps.Load()

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>Load Test Report</title>
    <script src="https://cdn.plot.ly/plotly-2.27.0.min.js"></script>
    <style>
        body { 
            font-family: Arial, sans-serif; 
            margin: 20px; 
            background: #f5f5f5;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
            background: white;
            padding: 30px;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        h1 { 
            color: #333; 
            border-bottom: 3px solid #4CAF50;
            padding-bottom: 10px;
        }
        h2 {
            color: #666;
            margin-top: 40px;
            border-bottom: 2px solid #ddd;
            padding-bottom: 8px;
        }
        .summary {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 20px;
            margin: 30px 0;
        }
        .metric-card {
            background: linear-gradient(135deg, #667eea 0%%, #764ba2 100%%);
            color: white;
            padding: 20px;
            border-radius: 8px;
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
        }
        .metric-card.green {
            background: linear-gradient(135deg, #11998e 0%%, #38ef7d 100%%);
        }
        .metric-card.blue {
            background: linear-gradient(135deg, #4facfe 0%%, #00f2fe 100%%);
        }
        .metric-card.orange {
            background: linear-gradient(135deg, #fa709a 0%%, #fee140 100%%);
        }
        .metric-card.red {
            background: linear-gradient(135deg, #f093fb 0%%, #f5576c 100%%);
        }
        .metric-label {
            font-size: 14px;
            opacity: 0.9;
            margin-bottom: 5px;
        }
        .metric-value {
            font-size: 32px;
            font-weight: bold;
        }
        .metric-unit {
            font-size: 16px;
            opacity: 0.8;
        }
        .chart {
            margin: 30px 0;
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 4px rgba(0,0,0,0.05);
        }
        .config {
            background: #f8f9fa;
            padding: 20px;
            border-radius: 8px;
            margin: 20px 0;
        }
        .config-item {
            display: flex;
            justify-content: space-between;
            padding: 8px 0;
            border-bottom: 1px solid #e0e0e0;
        }
        .config-item:last-child {
            border-bottom: none;
        }
        .config-label {
            font-weight: bold;
            color: #666;
        }
        .config-value {
            color: #333;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🚀 gRPC Load Test Report</h1>
        <p><strong>Generated:</strong> %s</p>
        
        <h2>📊 Summary</h2>
        <div class="summary">
            <div class="metric-card green">
                <div class="metric-label">Total Operations</div>
                <div class="metric-value">%d</div>
            </div>
            <div class="metric-card blue">
                <div class="metric-label">Average RPS</div>
                <div class="metric-value">%.0f</div>
                <div class="metric-unit">ops/sec</div>
            </div>
            <div class="metric-card orange">
                <div class="metric-label">Duration</div>
                <div class="metric-value">%.1f</div>
                <div class="metric-unit">seconds</div>
            </div>
            <div class="metric-card red">
                <div class="metric-label">Errors</div>
                <div class="metric-value">%d</div>
                <div class="metric-unit">%.2f%%%%</div>
            </div>
        </div>

        <div class="summary">
            <div class="metric-card" style="background: linear-gradient(135deg, #a8edea 0%%, #fed6e3 100%%);">
                <div class="metric-label">Set Operations</div>
                <div class="metric-value">%d</div>
            </div>
            <div class="metric-card" style="background: linear-gradient(135deg, #ffecd2 0%%, #fcb69f 100%%);">
                <div class="metric-label">Get Operations</div>
                <div class="metric-value">%d</div>
            </div>
            <div class="metric-card" style="background: linear-gradient(135deg, #ff9a9e 0%%, #fecfef 100%%);">
                <div class="metric-label">Apply Operations</div>
                <div class="metric-value">%d</div>
            </div>
            <div class="metric-card" style="background: linear-gradient(135deg, #fbc2eb 0%%, #a6c1ee 100%%);">
                <div class="metric-label">Delete Operations</div>
                <div class="metric-value">%d</div>
            </div>
        </div>

        <h2>⚙️ Configuration</h2>
        <div class="config">
            <div class="config-item">
                <span class="config-label">Server Address:</span>
                <span class="config-value">%s</span>
            </div>
            <div class="config-item">
                <span class="config-label">Workers:</span>
                <span class="config-value">%d</span>
            </div>
            <div class="config-item">
                <span class="config-label">Keys Count:</span>
                <span class="config-value">%d</span>
            </div>
            <div class="config-item">
                <span class="config-label">Read Ratio:</span>
                <span class="config-value">%.1f%%%%</span>
            </div>
            <div class="config-item">
                <span class="config-label">Counter Ratio:</span>
                <span class="config-value">%.1f%%%%</span>
            </div>
        </div>

        <h2>📈 Operations per Second</h2>
        <div id="rps-chart" class="chart"></div>

        <h2>⏱️ Latency Percentiles</h2>
        <div id="latency-chart" class="chart"></div>

        <h2>🔢 Cumulative Operations</h2>
        <div id="ops-chart" class="chart"></div>

        <script>
            const data = %s;
            
            // График RPS
            const rpsTrace1 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.set_rps),
                name: 'Set RPS',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#4CAF50', width: 2}
            };
            const rpsTrace2 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.get_rps),
                name: 'Get RPS',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#2196F3', width: 2}
            };
            const rpsTrace3 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.apply_rps),
                name: 'Apply RPS',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#FF9800', width: 2}
            };
            const rpsTrace4 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.delete_rps),
                name: 'Delete RPS',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#E91E63', width: 2}
            };
            const rpsTrace5 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.total_rps),
                name: 'Total RPS',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#9C27B0', width: 3, dash: 'dash'}
            };
            
            Plotly.newPlot('rps-chart', [rpsTrace1, rpsTrace2, rpsTrace3, rpsTrace4, rpsTrace5], {
                title: 'Requests per Second Over Time',
                xaxis: {title: 'Time (seconds)'},
                yaxis: {title: 'RPS'},
                hovermode: 'x unified'
            });
            
            // График латентности
            const latTrace1 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.latency_p50_us),
                name: 'p50',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#4CAF50', width: 2}
            };
            const latTrace2 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.latency_p95_us),
                name: 'p95',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#FF9800', width: 2}
            };
            const latTrace3 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.latency_p99_us),
                name: 'p99',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#F44336', width: 2}
            };
            const latTrace4 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.latency_max_us),
                name: 'max',
                type: 'scatter',
                mode: 'lines',
                line: {color: '#9C27B0', width: 2, dash: 'dot'}
            };
            
            Plotly.newPlot('latency-chart', [latTrace1, latTrace2, latTrace3, latTrace4], {
                title: 'Latency Percentiles Over Time',
                xaxis: {title: 'Time (seconds)'},
                yaxis: {title: 'Latency (μs)', type: 'log'},
                hovermode: 'x unified'
            });
            
            // График накопительных операций
            const opsTrace1 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.set_ops),
                name: 'Set',
                type: 'scatter',
                mode: 'lines',
                stackgroup: 'one',
                fillcolor: 'rgba(76, 175, 80, 0.5)',
                line: {color: '#4CAF50', width: 0}
            };
            const opsTrace2 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.get_ops),
                name: 'Get',
                type: 'scatter',
                mode: 'lines',
                stackgroup: 'one',
                fillcolor: 'rgba(33, 150, 243, 0.5)',
                line: {color: '#2196F3', width: 0}
            };
            const opsTrace3 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.apply_ops),
                name: 'Apply',
                type: 'scatter',
                mode: 'lines',
                stackgroup: 'one',
                fillcolor: 'rgba(255, 152, 0, 0.5)',
                line: {color: '#FF9800', width: 0}
            };
            const opsTrace4 = {
                x: data.map(d => d.elapsed_sec),
                y: data.map(d => d.delete_ops),
                name: 'Delete',
                type: 'scatter',
                mode: 'lines',
                stackgroup: 'one',
                fillcolor: 'rgba(233, 30, 99, 0.5)',
                line: {color: '#E91E63', width: 0}
            };
            
            Plotly.newPlot('ops-chart', [opsTrace1, opsTrace2, opsTrace3, opsTrace4], {
                title: 'Cumulative Operations',
                xaxis: {title: 'Time (seconds)'},
                yaxis: {title: 'Operations Count'},
                hovermode: 'x unified'
            });
        </script>
    </div>
</body>
</html>`,
		time.Now().Format("2006-01-02 15:04:05"),
		total,
		float64(total)/duration,
		duration,
		metrics.errors.Load(),
		float64(metrics.errors.Load())/float64(total)*100,
		metrics.setOps.Load(),
		metrics.getOps.Load(),
		metrics.applyOps.Load(),
		metrics.deleteOps.Load(),
		config.ServerAddr,
		config.Workers,
		config.KeysCount,
		config.ReadRatio*100,
		config.CounterRatio*100,
		prepareChartData(metrics),
	)

	return os.WriteFile(fmt.Sprintf("%s/report.html", dir), []byte(html), 0644)
}

func prepareChartData(metrics *Metrics) string {
	metrics.historyMux.Lock()
	defer metrics.historyMux.Unlock()

	type ChartData struct {
		ElapsedSec float64 `json:"elapsed_sec"`
		SetRPS     float64 `json:"set_rps"`
		GetRPS     float64 `json:"get_rps"`
		ApplyRPS   float64 `json:"apply_rps"`
		DeleteRPS  float64 `json:"delete_rps"`
		TotalRPS   float64 `json:"total_rps"`
		LatencyP50 float64 `json:"latency_p50_us"`
		LatencyP95 float64 `json:"latency_p95_us"`
		LatencyP99 float64 `json:"latency_p99_us"`
		LatencyMax float64 `json:"latency_max_us"`
		SetOps     uint64  `json:"set_ops"`
		GetOps     uint64  `json:"get_ops"`
		ApplyOps   uint64  `json:"apply_ops"`
		DeleteOps  uint64  `json:"delete_ops"`
	}

	chartData := make([]ChartData, len(metrics.history))
	for i, snap := range metrics.history {
		elapsed := snap.Timestamp.Sub(metrics.startTime).Seconds()
		chartData[i] = ChartData{
			ElapsedSec: elapsed,
			SetRPS:     snap.SetRPS,
			GetRPS:     snap.GetRPS,
			ApplyRPS:   snap.ApplyRPS,
			DeleteRPS:  snap.DeleteRPS,
			TotalRPS:   snap.TotalRPS,
			LatencyP50: float64(snap.LatencyP50) / 1000,
			LatencyP95: float64(snap.LatencyP95) / 1000,
			LatencyP99: float64(snap.LatencyP99) / 1000,
			LatencyMax: float64(snap.LatencyMax) / 1000,
			SetOps:     snap.SetOps,
			GetOps:     snap.GetOps,
			ApplyOps:   snap.ApplyOps,
			DeleteOps:  snap.DeleteOps,
		}
	}

	data, _ := json.Marshal(chartData)
	return string(data)
}

func main() {
	// Параметры командной строки
	addr := flag.String("addr", "localhost:50051", "gRPC server address")
	duration := flag.Duration("duration", 60*time.Second, "Длительность теста")
	workersN := flag.Int("workers", 10, "Количество воркеров")
	keysCount := flag.Int("keys", 10000, "Количество ключей")
	readRatio := flag.Float64("read-ratio", 0.7, "Доля read операций (0.0-1.0)")
	counterRatio := flag.Float64("counter-ratio", 0.5, "Доля counter среди write (0.0-1.0)")
	preWarmup := flag.Bool("warmup", true, "Прогрев хранилища перед тестом")
	warmupKeys := flag.Int("warmup-keys", 5000, "Количество ключей для прогрева")
	reportInterval := flag.Duration("report", 5*time.Second, "Интервал отчётов")
	outputDir := flag.String("output", "./reports", "Директория для сохранения отчётов")

	flag.Parse()

	config := &Config{
		ServerAddr:     *addr,
		Duration:       *duration,
		Workers:        *workersN,
		KeysCount:      *keysCount,
		ReadRatio:      *readRatio,
		CounterRatio:   *counterRatio,
		PreWarmup:      *preWarmup,
		WarmupKeys:     *warmupKeys,
		ReportInterval: *reportInterval,
	}

	// Подключение к gRPC
	conn, err := grpc.Dial(config.ServerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(10*1024*1024),
			grpc.MaxCallSendMsgSize(10*1024*1024),
		),
	)
	if err != nil {
		log.Fatalf("Не удалось подключиться: %v", err)
	}
	defer conn.Close()

	client := pb.NewLumeClient(conn)

	// Генерация ключей
	keys := make([]string, config.KeysCount)
	for i := 0; i < config.KeysCount; i++ {
		keys[i] = fmt.Sprintf("key_%d", i)
	}

	// Создание воркеров (до прогрева, чтобы заполнить их кеш типов)
	workers := make([]*Worker, config.Workers)
	for i := 0; i < config.Workers; i++ {
		workers[i] = NewWorker(i, client, config, keys)
	}

	// Прогрев
	if config.PreWarmup {
		warmupKeyList := keys
		if config.WarmupKeys < config.KeysCount {
			warmupKeyList = keys[:config.WarmupKeys]
		}
		warmup(client, warmupKeyList, config.CounterRatio, workers)
	}

	// Метрики (буфер для латентностей)
	metrics := NewMetrics(100000)

	// Запуск коллектора метрик
	metricsDone := make(chan struct{})
	go metricsCollector(metrics, config.ReportInterval, metricsDone)

	// Контекст для воркеров
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Запуск воркеров
	var wg sync.WaitGroup
	log.Printf("\nЗапуск %d воркеров на %v...\n", config.Workers, config.Duration)

	for i := 0; i < config.Workers; i++ {
		wg.Add(1)
		go workers[i].Run(ctx, metrics, &wg)
	}

	// Ожидание завершения
	wg.Wait()
	close(metricsDone)

	// Финальный отчёт
	printFinalReport(metrics)

	// Сохранение отчётов
	if err := saveReports(metrics, config, *outputDir); err != nil {
		log.Printf("⚠️  Ошибка сохранения отчётов: %v", err)
	}
}
