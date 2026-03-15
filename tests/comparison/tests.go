package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	client "github.com/kestfor/in-memorydb/tests/comparison/clients"
	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
)

func runClientGet(ctx context.Context, dbClient client.Client, keyPool *KeyPool) {

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

func runClientSet(ctx context.Context, dbClient client.Client, keyPool *KeyPool) {
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

		}
	}
}

func runTest(cfg Test, m *monitoring.Metrics) {

	testName := fmt.Sprintf("%s-%s", cfg.DB.Name, cfg.Name)

	slog.Info("Running Test", "name", testName, "config", cfg)

	var ctx = context.Background()
	currentClients := cfg.MinClients
	dbClient := client.GetClient(cfg.DB.Name, cfg.DB.Host, m)
	wgGroup := sync.WaitGroup{}

	testStage := 1
	totalStages := (cfg.MaxClients-cfg.MinClients)/cfg.ClientsStep + 1
	keysPool := preloadKeys(ctx, dbClient, cfg)

	for {
		slog.Info("Starting stage", "stage", testStage, "totalStages", totalStages, "clients", currentClients)
		ctx, cancel := context.WithCancel(ctx)
		m.SetStage(currentClients)

		go func() {
			time.Sleep(time.Duration(cfg.StageIntervalS) * time.Second)
			cancel()
		}()

		for i := 0; i < currentClients; i++ {
			wgGroup.Go(func() {
				runClientSet(ctx, dbClient, keysPool)
			})
		}

		wgGroup.Wait()

		slog.Info("Stage completed", "stage", testStage, "totalStages", totalStages, "clients", currentClients)
		testStage++

		if currentClients == cfg.MaxClients {
			break
		}

		currentClients += cfg.ClientsStep
	}
}
