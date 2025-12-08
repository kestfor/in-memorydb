package loadtest

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	pb "in-memorydb/api/lumepb"

	"google.golang.org/grpc"
)

type PreloadedKeys struct {
	ApplyGetKeys []string
	DeleteKeys   []string
}

// загружаем ключи заранее
func PreloadKeys(c pb.LumeClient, count int, t pb.Type) []string {
	keys := make([]string, count)

	for i := 0; i < count; i++ {
		k := RandomKey()
		_, err := c.Set(context.Background(), &pb.SetRequest{
			Key:      k,
			CrdtType: t,
		})
		if err != nil {
			fmt.Println("Preload error:", err)
			continue
		}
		keys[i] = k
	}
	return keys
}

func RunLoadTest(cfg LoadConfig) *Metrics {
	conn, err := grpc.Dial(cfg.TargetAddr, grpc.WithInsecure())
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	client := pb.NewLumeClient(conn)
	metrics := &Metrics{Latencies: make([]time.Duration, 0, 200000)}

	preload := PreloadedKeys{}

	// preload keys ONLY for Get / Apply tests
	if cfg.Type == TestGet || cfg.Type == TestApply || cfg.Type == TestMixed {
		preload.ApplyGetKeys = PreloadKeys(client, 50000, pb.Type_TYPE_PN_COUNTER)
	}

	// preload delete-only keys
	if cfg.Type == TestMixed {
		preload.DeleteKeys = PreloadKeys(client, 20000, pb.Type_TYPE_LWW_REGISTER)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	limiter := make(<-chan time.Time)
	if cfg.RateLimitRPS > 0 {
		limiter = time.Tick(time.Second / time.Duration(cfg.RateLimitRPS))
	}

	for i := 0; i < cfg.Concurrency; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if cfg.RateLimitRPS > 0 {
					<-limiter
				}

				runSingleOperation(ctx, client, cfg, metrics, preload)
			}
		}()
	}

	<-ctx.Done()
	time.Sleep(200 * time.Millisecond)

	return metrics
}

func runSingleOperation(
	ctx context.Context,
	c pb.LumeClient,
	cfg LoadConfig,
	metrics *Metrics,
	preload PreloadedKeys,
) {
	start := time.Now()
	var err error
	ok := true

	switch cfg.Type {

	case TestSet:
		_, err = c.Set(ctx, &pb.SetRequest{
			Key:      RandomKey(),
			CrdtType: pb.Type_TYPE_PN_COUNTER,
		})

	case TestApply:
		if len(preload.ApplyGetKeys) == 0 {
			return
		}
		key := preload.ApplyGetKeys[rand.Intn(len(preload.ApplyGetKeys))]

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
		key := preload.ApplyGetKeys[rand.Intn(len(preload.ApplyGetKeys))]

		_, err = c.Get(ctx, &pb.GetRequest{Key: key})

	case TestMixed:
		r := rand.Intn(100)

		switch {
		case r < cfg.MixedSetPct:
			_, err = c.Set(ctx, &pb.SetRequest{
				Key:      RandomKey(),
				CrdtType: pb.Type_TYPE_LWW_REGISTER,
			})

		case r < cfg.MixedSetPct+cfg.MixedGetPct:
			if len(preload.ApplyGetKeys) == 0 {
				return
			}
			key := preload.ApplyGetKeys[rand.Intn(len(preload.ApplyGetKeys))]
			_, err = c.Get(ctx, &pb.GetRequest{Key: key})

		case r < cfg.MixedSetPct+cfg.MixedGetPct+cfg.MixedApplyPct:
			if len(preload.ApplyGetKeys) == 0 {
				return
			}
			key := preload.ApplyGetKeys[rand.Intn(len(preload.ApplyGetKeys))]
			_, err = c.Apply(ctx, &pb.ApplyRequest{
				Key: key,
				Operation: &pb.ApplyRequest_RegisterOperation{
					RegisterOperation: &pb.ApplyRequest_Register{
						Value: RandomPayload(cfg.PayloadSize),
					},
				},
			})

		default:
			// DELETE в mixed работает только по отдельному списку
			if len(preload.DeleteKeys) == 0 {
				return
			}
			key := preload.DeleteKeys[rand.Intn(len(preload.DeleteKeys))]

			_, err = c.Delete(ctx, &pb.DeleteRequest{Key: key})
		}
	}

	if err != nil {
		ok = false
	}

	metrics.Record(time.Since(start), ok)
}
