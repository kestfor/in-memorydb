// lume-load — небольшой нагрузчик: вставляет указанное число случайных ключей
// через gRPC API. В отличие от lume-bench работает не во временном окне,
// а до ровного достижения --count и завершается.
//
// Пример:
//
//	lume-load -s localhost:8081 -n 1000000 -t register --value-size 64 -c 32
//	lume-load -s localhost:8081 -n 100000 -t counter -c 16
//	lume-load -s localhost:8081 -n 50000 -t mixed --key-prefix bulk_
package main

import (
	"context"
	"fmt"
	rand "math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/kestfor/in-memorydb/api/lume"
)

type opts struct {
	server     string
	count      int64
	keyType    string
	keyPrefix  string
	valueSize  int
	maxInc     int64
	conns      int // gRPC соединений (TCP)
	workers    int // воркеров на одно соединение
	timeoutSec int
	maxMsgSize int
	quiet      bool
	startIdx   int64
}

func main() {
	var o opts

	cmd := &cobra.Command{
		Use:   "lume-load",
		Short: "Bulk-insert random keys into Lume via gRPC",
		Long: `lume-load вставляет --count случайных ключей в Lume через gRPC API.

Тип ключа задаётся флагом --type:
  register  — LWW-регистр со случайным значением длины --value-size байт
  counter   — PN-счётчик, инициализированный случайным инкрементом [0, --max-inc]
  mixed     — 50/50 смесь register и counter

Ключи именуются как <key-prefix><i>, где i ∈ [start-index, start-index+count).
По умолчанию <key-prefix> = "key_", start-index = 0 — совместимо с тестами,
которые проверяют выборку ключей вида key_0, key_1, ...`,
		RunE: func(cmd *cobra.Command, args []string) error { return run(&o) },
	}
	cmd.SilenceUsage = true
	cmd.CompletionOptions.DisableDefaultCmd = true

	f := cmd.Flags()
	f.StringVarP(&o.server, "server", "s", "localhost:8081", "gRPC server address (host:port)")
	f.Int64VarP(&o.count, "count", "n", 10000, "Total number of keys to insert")
	f.StringVarP(&o.keyType, "type", "t", "register", "Key type: register | counter | mixed")
	f.StringVar(&o.keyPrefix, "key-prefix", "key_", "Key name prefix")
	f.Int64Var(&o.startIdx, "start-index", 0, "First key index (inclusive)")
	f.IntVar(&o.valueSize, "value-size", 16, "Register value size in bytes")
	f.Int64Var(&o.maxInc, "max-inc", 1_000_000, "Counter: random increment will be in [0, max-inc]")
	f.IntVarP(&o.conns, "conn", "c", 4, "Number of gRPC connections (TCP)")
	f.IntVarP(&o.workers, "workers", "w", 8, "Workers per connection (total parallelism = conn * workers)")
	f.IntVar(&o.timeoutSec, "req-timeout", 10, "Per-request timeout in seconds")
	f.IntVar(&o.maxMsgSize, "max-msg-size", 1<<30, "gRPC max message size in bytes")
	f.BoolVarP(&o.quiet, "quiet", "q", false, "Disable progress output")

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(o *opts) error {
	if o.count <= 0 {
		return fmt.Errorf("--count must be > 0")
	}
	if o.conns <= 0 {
		return fmt.Errorf("--conn must be > 0")
	}
	if o.workers <= 0 {
		return fmt.Errorf("--workers must be > 0")
	}
	switch o.keyType {
	case "register", "counter", "mixed":
	default:
		return fmt.Errorf("unknown --type %q (register | counter | mixed)", o.keyType)
	}

	// Открываем o.conns независимых gRPC-соединений (отдельные HTTP/2 потоки,
	// отдельные TCP-сокеты). На каждом — o.workers воркеров, делящих один client.
	conns := make([]*grpc.ClientConn, 0, o.conns)
	clients := make([]pb.LumeClient, 0, o.conns)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < o.conns; i++ {
		c, err := grpc.NewClient(
			o.server,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(
				grpc.MaxCallRecvMsgSize(o.maxMsgSize),
				grpc.MaxCallSendMsgSize(o.maxMsgSize),
			),
		)
		if err != nil {
			return fmt.Errorf("dial %s (conn %d): %w", o.server, i, err)
		}
		conns = append(conns, c)
		clients = append(clients, pb.NewLumeClient(c))
	}

	totalParallel := o.conns * o.workers
	fmt.Fprintf(os.Stderr,
		"lume-load: server=%s count=%d type=%s prefix=%q start=%d conn=%d workers=%d (total=%d)\n",
		o.server, o.count, o.keyType, o.keyPrefix, o.startIdx, o.conns, o.workers, totalParallel)

	var (
		ok       atomic.Int64
		failed   atomic.Int64
		next     atomic.Int64 // следующий индекс для воркера
		startTs  = time.Now()
		stopProg = make(chan struct{})
		wgProg   sync.WaitGroup
	)

	if !o.quiet {
		wgProg.Add(1)
		go func() {
			defer wgProg.Done()
			progressLoop(stopProg, &ok, &failed, o.count, startTs)
		}()
	}

	var wg sync.WaitGroup
	wg.Add(totalParallel)
	for ci := 0; ci < o.conns; ci++ {
		client := clients[ci]
		for wi := 0; wi < o.workers; wi++ {
			workerID := ci*o.workers + wi
			seed := rand.Uint64() ^ uint64(workerID+1)
			go func(client pb.LumeClient, seed uint64) {
				defer wg.Done()
				rng := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
				for {
					idx := next.Add(1) - 1
					if idx >= o.count {
						return
					}
					key := fmt.Sprintf("%s%d", o.keyPrefix, o.startIdx+idx)

					ctx, cancel := context.WithTimeout(context.Background(),
						time.Duration(o.timeoutSec)*time.Second)
					err := insertOne(ctx, client, key, pickType(o.keyType, rng), o, rng)
					cancel()
					if err != nil {
						failed.Add(1)
					} else {
						ok.Add(1)
					}
				}
			}(client, seed)
		}
	}

	wg.Wait()
	close(stopProg)
	wgProg.Wait()

	elapsed := time.Since(startTs)
	rate := float64(ok.Load()) / elapsed.Seconds()
	fmt.Fprintf(os.Stderr,
		"\ndone: ok=%d failed=%d elapsed=%.2fs rate=%.0f keys/s\n",
		ok.Load(), failed.Load(), elapsed.Seconds(), rate)
	if failed.Load() > 0 {
		os.Exit(2)
	}
	return nil
}

func pickType(t string, rng *rand.Rand) string {
	if t != "mixed" {
		return t
	}
	if rng.IntN(2) == 0 {
		return "register"
	}
	return "counter"
}

// insertOne делает один Set + один Apply, чтобы у ключа было реальное значение
// (Set без Apply создаёт пустой CRDT). Это совпадает с поведением lume-cli set.
func insertOne(ctx context.Context, client pb.LumeClient, key, kind string, o *opts, rng *rand.Rand) error {
	switch kind {
	case "register":
		val := randBytes(rng, o.valueSize)
		if _, err := client.Set(ctx, &pb.SetRequest{
			Key:      key,
			CrdtType: pb.Type_TYPE_LWW_REGISTER,
		}); err != nil {
			return fmt.Errorf("set register %s: %w", key, err)
		}
		_, err := client.Apply(ctx, &pb.ApplyRequest{
			Key: key,
			Operation: &pb.ApplyRequest_RegisterOperation{
				RegisterOperation: &pb.ApplyRequest_Register{Value: val},
			},
		})
		if err != nil {
			return fmt.Errorf("apply register %s: %w", key, err)
		}
		return nil

	case "counter":
		inc := rng.Int64N(o.maxInc + 1)
		if _, err := client.Set(ctx, &pb.SetRequest{
			Key:      key,
			CrdtType: pb.Type_TYPE_PN_COUNTER,
		}); err != nil {
			return fmt.Errorf("set counter %s: %w", key, err)
		}
		_, err := client.Apply(ctx, &pb.ApplyRequest{
			Key: key,
			Operation: &pb.ApplyRequest_CounterOperationInc{
				CounterOperationInc: &pb.ApplyRequest_CounterInc{Val: inc},
			},
		})
		if err != nil {
			return fmt.Errorf("apply counter %s: %w", key, err)
		}
		return nil
	}
	return fmt.Errorf("unknown kind %q", kind)
}

func randBytes(rng *rand.Rand, n int) []byte {
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	// Печатные ASCII, чтобы значение легко глазами читалось при отладке.
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range b {
		b[i] = alphabet[rng.IntN(len(alphabet))]
	}
	return b
}

func progressLoop(stop <-chan struct{}, ok, failed *atomic.Int64, total int64, startTs time.Time) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	prevOk := int64(0)
	prevTs := startTs
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			cur := ok.Load()
			fl := failed.Load()
			dt := now.Sub(prevTs).Seconds()
			instRate := 0.0
			if dt > 0 {
				instRate = float64(cur-prevOk) / dt
			}
			pct := 0.0
			if total > 0 {
				pct = 100 * float64(cur+fl) / float64(total)
			}
			fmt.Fprintf(os.Stderr, "\r  %6.2f%%  ok=%d  fail=%d  rate=%.0f keys/s   ",
				pct, cur, fl, instRate)
			prevOk = cur
			prevTs = now
		}
	}
}
