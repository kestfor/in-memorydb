package loadtest

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	pb "github.com/kestfor/in-memorydb/api/lumepb"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type PreloadedKeys struct {
	ApplyGetKeys []string
	DeleteKeys   []string
}

// загружаем ключи заранее
func PreloadKeys(c pb.LumeClient, count int, t pb.Type) []string {
	return PreloadKeysWithProgress(c, count, t, false)
}

func PreloadKeysWithProgress(c pb.LumeClient, count int, t pb.Type, showProgress bool) []string {
	if count == 0 {
		return nil
	}

	if showProgress {
		fmt.Printf("Preloading %d keys...\n", count)
	}

	keys := make([]string, count)
	concurrency := 50

	type result struct {
		idx int
		key string
	}

	resultsCh := make(chan result, concurrency)
	workCh := make(chan int, count)

	// Заполняем канал работы
	for i := 0; i < count; i++ {
		workCh <- i
	}
	close(workCh)

	// Запускаем worker'ы
	for w := 0; w < concurrency; w++ {
		go func() {
			for idx := range workCh {
				k := RandomKey()
				_, err := c.Set(context.Background(), &pb.SetRequest{
					Key:      k,
					CrdtType: t,
				})
				if err != nil {
					// Retry once
					_, err = c.Set(context.Background(), &pb.SetRequest{
						Key:      k,
						CrdtType: t,
					})
				}
				resultsCh <- result{idx: idx, key: k}
			}
		}()
	}

	// Собираем результаты
	completed := 0
	for completed < count {
		r := <-resultsCh
		keys[r.idx] = r.key
		completed++

		if showProgress && completed%1000 == 0 {
			fmt.Printf("\rPreloading: %d/%d (%.1f%%)", completed, count, float64(completed)/float64(count)*100)
		}
	}

	if showProgress {
		fmt.Printf("\rPreloading: %d/%d (100.0%%) - Done!\n", count, count)
	}

	return keys
}

func RunLoadTest(cfg LoadConfig) *Metrics {
	return RunLoadTestWithProgress(cfg, false)
}

func RunLoadTestWithProgress(cfg LoadConfig, showProgress bool) *Metrics {
	conn, err := grpc.Dial(cfg.TargetAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	client := pb.NewLumeClient(conn)
	metrics := NewMetrics()

	preload := PreloadedKeys{}

	// preload keys ONLY for Get / Apply tests
	if cfg.Type == TestGet || cfg.Type == TestApply || cfg.Type == TestMixed {
		preload.ApplyGetKeys = PreloadKeysWithProgress(client, 50000, pb.Type_TYPE_PN_COUNTER, showProgress)
	}

	// preload delete-only keys
	if cfg.Type == TestMixed {
		preload.DeleteKeys = PreloadKeysWithProgress(client, 20000, pb.Type_TYPE_LWW_REGISTER, showProgress)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	// Правильный rate limiter с golang.org/x/time/rate
	// burst = min(concurrency, RPS/10) для равномерного распределения
	var limiter *rate.Limiter
	if cfg.RateLimitRPS > 0 {
		burst := cfg.Concurrency
		if burst > cfg.RateLimitRPS/10 && cfg.RateLimitRPS >= 10 {
			burst = cfg.RateLimitRPS / 10
		}
		if burst < 1 {
			burst = 1
		}
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimitRPS), burst)
	}

	// Запускаем worker'ы
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Thread-safe random generator для каждой горутины
			rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))

			for {
				// Проверяем контекст ДО ожидания limiter
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Rate limiting с проверкой контекста
				if limiter != nil {
					if err := limiter.Wait(ctx); err != nil {
						return // контекст отменён, выходим
					}
				}

				// Ещё раз проверяем после wait
				select {
				case <-ctx.Done():
					return
				default:
				}

				runSingleOperation(ctx, client, cfg, metrics, preload, rng)
			}
		}(i)
	}

	// Реалтайм прогресс
	if showProgress {
		progressTicker := time.NewTicker(1 * time.Second)
		defer progressTicker.Stop()

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-progressTicker.C:
					PrintProgress(metrics.Snapshot())
				}
			}
		}()
	}

	<-ctx.Done()

	// Ждем завершения всех worker'ов
	wg.Wait()

	if showProgress {
		ClearLine()
	}

	// Финализируем метрики (flush буфера)
	metrics.Finish()

	return metrics
}

func runSingleOperation(
	ctx context.Context,
	c pb.LumeClient,
	cfg LoadConfig,
	metrics *Metrics,
	preload PreloadedKeys,
	rng *rand.Rand,
) {
	start := time.Now()
	var err error
	ok := true

	switch cfg.Type {

	case TestSet:
		_, err = c.Set(ctx, &pb.SetRequest{
			Key:      RandomKeyWithRng(rng),
			CrdtType: pb.Type_TYPE_PN_COUNTER,
		})

	case TestApply:
		if len(preload.ApplyGetKeys) == 0 {
			return
		}
		key := preload.ApplyGetKeys[rng.Intn(len(preload.ApplyGetKeys))]

		_, err = c.Apply(ctx, &pb.ApplyRequest{
			Key: key,
			Operation: &pb.ApplyRequest_CounterOperationInc{
				CounterOperationInc: &pb.ApplyRequest_CounterInc{
					Val: cfg.CounterStep,
				},
			},
		})

	case TestGet:
		if len(preload.ApplyGetKeys) == 0 {
			return
		}
		key := preload.ApplyGetKeys[rng.Intn(len(preload.ApplyGetKeys))]

		_, err = c.Get(ctx, &pb.GetRequest{Key: key})

	case TestMixed:
		r := rng.Intn(100)

		switch {
		case r < cfg.MixedSetPct:
			_, err = c.Set(ctx, &pb.SetRequest{
				Key:      RandomKeyWithRng(rng),
				CrdtType: pb.Type_TYPE_LWW_REGISTER,
			})

		case r < cfg.MixedSetPct+cfg.MixedGetPct:
			if len(preload.ApplyGetKeys) == 0 {
				return
			}
			key := preload.ApplyGetKeys[rng.Intn(len(preload.ApplyGetKeys))]
			_, err = c.Get(ctx, &pb.GetRequest{Key: key})

		case r < cfg.MixedSetPct+cfg.MixedGetPct+cfg.MixedApplyPct:
			if len(preload.ApplyGetKeys) == 0 {
				return
			}
			key := preload.ApplyGetKeys[rng.Intn(len(preload.ApplyGetKeys))]
			_, err = c.Apply(ctx, &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_RegisterOperation{
					RegisterOperation: &pb.ApplyRequest_Register{
						Value: RandomPayloadWithRng(rng, cfg.PayloadSize),
					},
				},
			})

		default:
			// DELETE в mixed работает только по отдельному списку
			if len(preload.DeleteKeys) == 0 {
				return
			}
			key := preload.DeleteKeys[rng.Intn(len(preload.DeleteKeys))]

			_, err = c.Delete(ctx, &pb.DeleteRequest{Key: key})
		}
	}

	// Не считаем ошибки отмены контекста как failed requests
	if err != nil {
		if ctx.Err() != nil {
			return // контекст отменён, не записываем эту операцию
		}
		ok = false
	}

	metrics.Record(time.Since(start), ok)
}
