package main

import (
	"context"
	"fmt"
	rand "math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	lumepb "github.com/kestfor/in-memorydb/api/lume"
	"github.com/kestfor/in-memorydb/cmd/lume-bench/syscall"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/benchmark/stats"
	"google.golang.org/grpc/credentials/insecure"
)

// ANSI colors.
const (
	bold    = "\033[1m"
	dim     = "\033[2m"
	reset   = "\033[0m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	cyan    = "\033[36m"
	boldCyn = bold + cyan
	boldGrn = bold + green
)

var hopts = stats.HistogramOptions{
	NumBuckets:   2495,
	GrowthFactor: .01,
}

type config struct {
	ports    []string
	numRPC   int
	numConn  int
	warmup   int
	duration int
	reqSize  int
	reqType  string
	poolSize int
	testName string
	outDir   string
}

var cfg config

func main() {
	cmd := &cobra.Command{
		Use:   "lume-bench",
		Short: "Benchmark client for lume gRPC server",
		RunE:  run,
	}
	cmd.CompletionOptions.DisableDefaultCmd = true
	cmd.SilenceUsage = true

	f := cmd.Flags()
	f.StringSliceVarP(&cfg.ports, "ports", "p", []string{"8080"}, "Ports to connect to (on localhost)")
	f.IntVarP(&cfg.numRPC, "rpc", "r", 1, "Concurrent RPCs per connection")
	f.IntVarP(&cfg.numConn, "conn", "c", 1, "Parallel connections per port")
	f.IntVarP(&cfg.warmup, "warmup", "w", 10, "Warm-up duration (seconds)")
	f.IntVarP(&cfg.duration, "duration", "d", 60, "Benchmark duration (seconds)")
	f.IntVar(&cfg.reqSize, "req-size", 1, "Request payload size (bytes)")
	f.StringVarP(&cfg.reqType, "type", "t", "mixed", "Request type: get, set, or mixed")
	f.IntVar(&cfg.poolSize, "pool-size", 10000, "Key pool size")
	f.StringVarP(&cfg.testName, "name", "n", "", "Test name (for profile filenames, default: bench_<timestamp>)")
	f.StringVarP(&cfg.outDir, "out", "o", ".", "Output directory for profile files")

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(_ *cobra.Command, _ []string) error {
	if cfg.testName == "" {
		cfg.testName = "bench_" + time.Now().Format("2006-01-02_15-04-05")
	}
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	switch cfg.reqType {
	case "get", "set", "mixed":
	default:
		return fmt.Errorf("invalid request type %q: must be get, set, or mixed", cfg.reqType)
	}

	totalConns := cfg.numConn * len(cfg.ports)
	totalWorkers := totalConns * cfg.numRPC

	fmt.Println(boldCyn + "========== lume-bench ==========\n" + reset)
	fmt.Printf("  "+dim+"ports:      	  "+reset+" %v\n", cfg.ports)
	fmt.Printf("  "+dim+"connections:	  "+reset+" %d (%d/port x %d ports)\n", totalConns, cfg.numConn, len(cfg.ports))
	fmt.Printf("  "+dim+"workers:    	  "+reset+" %d (%d/conn)\n", totalWorkers, cfg.numRPC)
	fmt.Printf("  "+dim+"warmup:     	  "+reset+" %ds\n", cfg.warmup)
	fmt.Printf("  "+dim+"duration:   	  "+reset+" %ds\n", cfg.duration)
	fmt.Printf("  "+dim+"req type:       "+reset+" %s\n", cfg.reqType)
	fmt.Printf("  "+dim+"key pool size:  "+reset+" %d\n", cfg.poolSize)
	fmt.Printf("  "+dim+"req body size:  "+reset+" %d bytes\n", cfg.reqSize)
	fmt.Println()

	// Connect
	fmt.Printf(blue+"Connecting"+reset+" to %d port(s)... ", len(cfg.ports))
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer connectCancel()
	ccs, err := connect(connectCtx)
	if err != nil {
		return err
	}
	defer closeAll(ccs)
	fmt.Printf(green+"%d connection(s) ready"+reset+"\n", len(ccs))

	// Key pool
	pool := newKeyPool(cfg.poolSize)

	// Pre-populate
	if cfg.reqType == "get" || cfg.reqType == "mixed" {
		if err := prepopulate(ccs[0], pool); err != nil {
			return fmt.Errorf("pre-populate: %w", err)
		}
	}

	// CPU profile
	cf, err := os.Create(filepath.Join(cfg.outDir, cfg.testName+".cpu"))
	if err != nil {
		return fmt.Errorf("create cpu profile: %w", err)
	}
	defer cf.Close()
	_ = pprof.StartCPUProfile(cf)
	cpuBeg := syscall.GetCPUTime()

	// Benchmark
	fmt.Println()
	warmEnd := time.Now().Add(time.Duration(cfg.warmup) * time.Second)
	benchEnd := warmEnd.Add(time.Duration(cfg.duration) * time.Second)

	var ops, errs atomic.Int64
	progressCtx, progressStop := context.WithCancel(context.Background())
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		reportProgress(progressCtx, warmEnd, &ops, &errs)
	}()

	var wg sync.WaitGroup
	histCh := make(chan *stats.Histogram, totalWorkers)
	for _, cc := range ccs {
		for range cfg.numRPC {
			wg.Add(1)
			go func() {
				defer wg.Done()
				histCh <- runWorker(cc, pool, warmEnd, benchEnd, &ops, &errs)
			}()
		}
	}
	wg.Wait()
	progressStop()
	progressWg.Wait()
	close(histCh)

	// CPU time & profile
	cpuTime := time.Duration(syscall.GetCPUTime() - cpuBeg)
	pprof.StopCPUProfile()

	// Memory profile
	mf, err := os.Create(filepath.Join(cfg.outDir, cfg.testName+".mem"))
	if err != nil {
		return fmt.Errorf("create mem profile: %w", err)
	}
	defer mf.Close()
	runtime.GC()
	if err := pprof.WriteHeapProfile(mf); err != nil {
		return fmt.Errorf("write mem profile: %w", err)
	}

	// Merge histograms & print results
	merged := stats.NewHistogram(hopts)
	for h := range histCh {
		merged.Merge(h)
	}

	fmt.Println("\n" + boldCyn + "========== Results ==========" + reset)
	printResults(merged, cpuTime, cf.Name(), mf.Name())
	return nil
}

func connect(ctx context.Context) ([]*grpc.ClientConn, error) {
	ccs := make([]*grpc.ClientConn, 0, cfg.numConn*len(cfg.ports))
	for _, port := range cfg.ports {
		for range cfg.numConn {
			cc, err := grpc.DialContext(ctx, "localhost:"+port,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
				grpc.WithWriteBufferSize(128*1024),
				grpc.WithReadBufferSize(128*1024),
			)
			if err != nil {
				closeAll(ccs)
				return nil, fmt.Errorf("dial localhost:%s: %w", port, err)
			}
			ccs = append(ccs, cc)
		}
	}
	return ccs, nil
}

func closeAll(ccs []*grpc.ClientConn) {
	for _, cc := range ccs {
		cc.Close()
	}
}

func prepopulate(cc *grpc.ClientConn, pool *keyPool) error {
	fmt.Printf(blue+"Pre-populating"+reset+" %d keys...\n", cfg.poolSize)
	client := lumepb.NewLumeClient(cc)
	for i, key := range pool.keys {
		if _, err := client.Set(context.Background(), &lumepb.SetRequest{
			Key:      key,
			CrdtType: lumepb.Type_TYPE_LWW_REGISTER,
		}); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
		if _, err := client.Apply(context.Background(), &lumepb.ApplyRequest{
			Key: key,
			Operation: &lumepb.ApplyRequest_RegisterOperation{
				RegisterOperation: &lumepb.ApplyRequest_Register{
					Value: make([]byte, cfg.reqSize),
				},
			},
		}); err != nil {
			return fmt.Errorf("apply %s: %w", key, err)
		}
		if (i+1)%2000 == 0 {
			fmt.Printf("\r\033[K  "+dim+"%d/%d keys"+reset, i+1, len(pool.keys))
		}
	}
	fmt.Printf("\r\033[K  %d/%d keys — "+green+"done"+reset+"\n", len(pool.keys), len(pool.keys))
	return nil
}

func reportProgress(ctx context.Context, warmEnd time.Time, ops, errs *atomic.Int64) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var prevOps int64
	prevTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			curr := ops.Load()
			dt := now.Sub(prevTime).Seconds()
			rps := float64(curr-prevOps) / dt
			prevOps = curr
			prevTime = now

			if now.Before(warmEnd) {
				left := warmEnd.Sub(now).Truncate(time.Second)
				fmt.Printf("\r\033[K  "+yellow+"[warmup]"+reset+" %s left "+dim+"|"+reset+" rps: "+bold+"%.0f"+reset, left, rps)
			} else {
				errCount := errs.Load()
				errColor := dim
				if errCount > 0 {
					errColor = red
				}
				fmt.Printf("\r\033[K  "+green+"[bench]"+reset+"  ops: "+bold+"%d"+reset+" "+dim+"|"+reset+" rps: "+boldGrn+"%.0f"+reset+" "+dim+"|"+reset+" errors: "+errColor+"%d"+reset,
					curr, rps, errCount)
			}
		}
	}
}

func runWorker(cc *grpc.ClientConn, pool *keyPool, warmEnd, benchEnd time.Time, ops, errs *atomic.Int64) *stats.Histogram {
	client := lumepb.NewLumeClient(cc)
	hist := stats.NewHistogram(hopts)

	for {
		start := time.Now()
		if start.After(benchEnd) {
			return hist
		}
		err := doRequest(client, pool)
		elapsed := time.Since(start)
		if err != nil {
			errs.Add(1)
		} else {
			ops.Add(1)
			if start.After(warmEnd) {
				_ = hist.Add(elapsed.Nanoseconds())
			}
		}
	}
}

func doRequest(client lumepb.LumeClient, pool *keyPool) error {
	key := pool.randomKey()
	op := cfg.reqType
	if op == "mixed" {
		if rand.IntN(100) < 70 {
			op = "get"
		} else {
			op = "set"
		}
	}
	switch op {
	case "get":
		_, err := client.Get(context.Background(), &lumepb.GetRequest{Key: key})
		return err
	default:
		_, err := client.Set(context.Background(), &lumepb.SetRequest{
			Key:      key,
			CrdtType: lumepb.Type_TYPE_LWW_REGISTER,
		})
		return err
	}
}

func printResults(hist *stats.Histogram, cpuTime time.Duration, cpuProfile, memProfile string) {
	fmt.Printf("  "+dim+"QPS:        "+reset+" "+boldGrn+"%.0f"+reset+"\n", float64(hist.Count)/float64(cfg.duration))
	fmt.Printf("  "+dim+"Latency:   "+reset+" p50="+green+"%v"+reset+"  p90="+yellow+"%v"+reset+"  p99="+red+"%v"+reset+"\n",
		time.Duration(percentile(0.5, hist)),
		time.Duration(percentile(0.9, hist)),
		time.Duration(percentile(0.99, hist)),
	)
	fmt.Printf("  "+dim+"CPU time:  "+reset+" %v\n", cpuTime)
	fmt.Printf("  "+dim+"CPU profile:"+reset+" %s\n", cpuProfile)
	fmt.Printf("  "+dim+"Mem profile:"+reset+" %s\n", memProfile)
}

func percentile(p float64, h *stats.Histogram) int64 {
	need := int64(float64(h.Count) * p)
	var have int64
	for _, bucket := range h.Buckets {
		if have+bucket.Count >= need {
			frac := float64(need-have) / float64(bucket.Count)
			return int64((1.0-frac)*bucket.LowBound + frac*bucket.LowBound*(1.0+hopts.GrowthFactor))
		}
		have += bucket.Count
	}
	panic("percentile: histogram underflow")
}

type keyPool struct {
	keys []string
}

func newKeyPool(size int) *keyPool {
	kp := &keyPool{keys: make([]string, size)}
	for i := range kp.keys {
		kp.keys[i] = "key_" + strconv.Itoa(i)
	}
	return kp
}

func (kp *keyPool) randomKey() string {
	return kp.keys[rand.IntN(len(kp.keys))]
}
