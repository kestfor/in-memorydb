package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	client "github.com/kestfor/in-memorydb/tests/comparison/clients"
	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
)

func maybeSleep(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runClientGet(ctx context.Context, dbClient client.Client, keyPool *KeyPool, requestDelay time.Duration) {

	for {
		select {
		case <-ctx.Done():
			return
		default:
			u := keyPool.GetKey()

			_, err := dbClient.Get(ctx, u)
			if err != nil {
				slog.Warn("get failed", "err", err)
			}

			if !maybeSleep(ctx, requestDelay) {
				return
			}
		}
	}
}

func preloadKeys(ctx context.Context, dbClient client.Client, cfg Test) *KeyPool {
	pool := NewKeyPool()

	for i := 0; i < cfg.MaxKeysNum; i++ {
		u := models.NewUser()
		if err := dbClient.Set(ctx, u.Uuid, u); err != nil {
			slog.Warn("set failed", "err", err)
			continue
		}
		pool.Put(u.Uuid, u)
	}

	return pool
}

func runClientSet(ctx context.Context, dbClient client.Client, keyPool *KeyPool, requestDelay time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			u := keyPool.GetObj()
			err := dbClient.Set(ctx, u.Uuid, u)
			if err != nil {
				slog.Warn("set failed", "err", err)
			}

			if !maybeSleep(ctx, requestDelay) {
				return
			}
		}
	}
}

func getTestFunc(cfg Test) func(ctx context.Context, dbClient client.Client, keyPool *KeyPool, requestDelay time.Duration) {
	if cfg.Type == "set" {
		return runClientSet
	} else if cfg.Type == "get" {
		return runClientGet
	}
	panic("unknown test type")
}

func buildClientStages(cfg Test) []int {
	if len(cfg.ClientStages) > 0 {
		stages := make([]int, 0, len(cfg.ClientStages))
		prev := -1
		for _, stage := range cfg.ClientStages {
			if stage <= 0 {
				continue
			}
			if stage == prev {
				continue
			}
			stages = append(stages, stage)
			prev = stage
		}
		if len(stages) > 0 {
			return stages
		}
	}

	if cfg.MinClients <= 0 || cfg.MaxClients <= 0 || cfg.MinClients > cfg.MaxClients {
		panic("invalid clients range")
	}

	if cfg.GrowthMode == "exponential" || cfg.GrowthMode == "log" {
		factor := cfg.GrowthFactor
		if factor <= 1 {
			factor = 2
		}

		stages := []int{cfg.MinClients}
		current := float64(cfg.MinClients)
		for {
			next := int(math.Ceil(current * factor))
			if next <= stages[len(stages)-1] {
				next = stages[len(stages)-1] + 1
			}
			if next >= cfg.MaxClients {
				break
			}
			stages = append(stages, next)
			current = float64(next)
		}

		if stages[len(stages)-1] != cfg.MaxClients {
			stages = append(stages, cfg.MaxClients)
		}

		return stages
	}

	step := cfg.ClientsStep
	if step <= 0 {
		step = 1
	}

	totalStages := (cfg.MaxClients-cfg.MinClients)/step + 1
	stages := make([]int, 0, totalStages)
	for current := cfg.MinClients; current <= cfg.MaxClients; current += step {
		stages = append(stages, current)
	}
	if stages[len(stages)-1] != cfg.MaxClients {
		stages = append(stages, cfg.MaxClients)
	}

	return stages
}

func runTest(cfg Test, m *monitoring.Metrics, csv *monitoring.CSVExporter) {

	testFunc := getTestFunc(cfg)
	requestDelay := time.Duration(cfg.RequestDelayMs) * time.Millisecond
	clientStages := buildClientStages(cfg)

	testName := fmt.Sprintf("%s-%s", cfg.DB.Name, cfg.Name)

	slog.Info("Running Test", "name", testName, "config", cfg)

	var ctx = context.Background()
	dbClient := client.GetClient(cfg.DB.Name, cfg.DB.Host, m)
	wgGroup := sync.WaitGroup{}

	testStage := 1
	totalStages := len(clientStages)
	keysPool := preloadKeys(ctx, dbClient, cfg)

	for _, currentClients := range clientStages {
		slog.Info("Starting stage", "stage", testStage, "totalStages", totalStages, "clients", currentClients)
		ctx, cancel := context.WithCancel(ctx)
		m.SetStage(currentClients)

		stageStart := time.Now()
		go func() {
			time.Sleep(time.Duration(cfg.StageIntervalS) * time.Second)
			cancel()
		}()

		for i := 0; i < currentClients; i++ {
			wgGroup.Go(func() {
				testFunc(ctx, dbClient, keysPool, requestDelay)
			})
		}

		wgGroup.Wait()
		stageDuration := time.Since(stageStart).Seconds()

		slog.Info("Stage completed", "stage", testStage, "totalStages", totalStages, "clients", currentClients)

		if err := csv.RecordStage(cfg.DB.Name, currentClients, stageDuration); err != nil {
			slog.Warn("csv export failed", "err", err)
		}

		testStage++
	}
}
