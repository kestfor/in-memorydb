/*
 *
 * Copyright 2017 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

/*
Package main provides a client used for benchmarking.  Before running the
client, the user would need to launch the grpc server.

To start the server before running the client, you can run look for the command
under the following file:

	benchmark/server/main.go

After starting the server, the client can be run.  An example of how to run this
command is:

go run benchmark/client/main.go -test_name=grpc_test

If the server is running on a different port than 50051, then use the port flag
for the client to hit the server on the correct port.
An example for how to run this command on a different port can be found here:

go run benchmark/client/main.go -test_name=grpc_test -port=8080
*/
package main

import (
	"context"
	"flag"
	"fmt"
	rand "math/rand/v2"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync"
	"time"

	lumepb "github.com/kestfor/in-memorydb/api/lume"
	"github.com/kestfor/in-memorydb/cmd/lume-bench/syscall"
	"google.golang.org/grpc"
	"google.golang.org/grpc/benchmark"
	"google.golang.org/grpc/benchmark/stats"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/grpclog"
)

var (
	port      = flag.String("port", "50051", "Localhost port to connect to.")
	numRPC    = flag.Int("r", 1, "The number of concurrent RPCs on each connection.")
	numConn   = flag.Int("c", 1, "The number of parallel connections.")
	warmupDur = flag.Int("w", 10, "Warm-up duration in seconds")
	duration  = flag.Int("d", 60, "Benchmark duration in seconds")
	rqSize    = flag.Int("req", 1, "Request message size in bytes.")
	rspSize   = flag.Int("resp", 1, "Response message size in bytes.")
	rpcType   = flag.String("rpc_type", "unary",
		`Configure different client rpc type. Valid options are:
		   unary;
		   streaming.`)
	reqType  = flag.String("request_type", "mixed", "Request type: get, set, or mixed")
	poolSize = flag.Int("pool_size", 100000, "Size of the key pool")
	testName = flag.String("test_name", "", "Name of the test used for creating profiles.")
	wg       sync.WaitGroup
	hopts    = stats.HistogramOptions{
		NumBuckets:   2495,
		GrowthFactor: .01,
	}
	mu    sync.Mutex
	hists []*stats.Histogram

	logger = grpclog.Component("benchmark")
)

// keyPool generates keys for the fixed pool
type keyPool struct {
	keys []string
}

func newKeyPool(size int) *keyPool {
	kp := &keyPool{
		keys: make([]string, size),
	}
	for i := 0; i < size; i++ {
		kp.keys[i] = "key_" + strconv.Itoa(i)
	}
	return kp
}

func (kp *keyPool) randomKey() string {
	return kp.keys[rand.IntN(len(kp.keys))]
}

func main() {
	flag.Parse()
	if *testName == "" {
		logger.Fatal("-test_name not set")
	}

	// Validate request type
	switch *reqType {
	case "get", "set", "mixed":
	default:
		logger.Fatalf("Invalid request_type: %s. Must be one of: get, set, mixed", *reqType)
	}

	connectCtx, connectCancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Second))
	defer connectCancel()
	ccs := buildConnections(connectCtx)

	// Initialize key pool
	pool := newKeyPool(*poolSize)

	// Pre-populate keys if needed (for get requests to work)
	if *reqType == "get" || *reqType == "mixed" {
		logger.Infof("Pre-populating %d keys...", *poolSize)
		client := lumepb.NewLumeClient(ccs[0])
		for _, key := range pool.keys {
			// Create LWW-Register with initial value
			_, err := client.Set(context.Background(), &lumepb.SetRequest{
				Key:      key,
				CrdtType: lumepb.Type_TYPE_LWW_REGISTER,
			})
			if err != nil {
				logger.Warningf("Failed to pre-populate key %s: %v", key, err)
			}
			// Set initial value
			_, err = client.Apply(context.Background(), &lumepb.ApplyRequest{
				Key: key,
				Operation: &lumepb.ApplyRequest_RegisterOperation{
					RegisterOperation: &lumepb.ApplyRequest_Register{
						Value: make([]byte, *rqSize),
					},
				},
			})
			if err != nil {
				logger.Warningf("Failed to set initial value for key %s: %v", key, err)
			}
		}
		logger.Info("Pre-population complete")
	}

	warmDeadline := time.Now().Add(time.Duration(*warmupDur) * time.Second)
	endDeadline := warmDeadline.Add(time.Duration(*duration) * time.Second)
	cf, err := os.Create("/tmp/" + *testName + ".cpu")
	if err != nil {
		logger.Fatalf("Error creating file: %v", err)
	}
	defer cf.Close()
	_ = pprof.StartCPUProfile(cf)
	cpuBeg := syscall.GetCPUTime()

	for _, cc := range ccs {
		runWithConn(cc, pool, warmDeadline, endDeadline)
	}

	wg.Wait()
	cpu := time.Duration(syscall.GetCPUTime() - cpuBeg)
	pprof.StopCPUProfile()
	mf, err := os.Create("/tmp/" + *testName + ".mem")
	if err != nil {
		logger.Fatalf("Error creating file: %v", err)
	}
	defer mf.Close()
	runtime.GC() // materialize all statistics
	if err := pprof.WriteHeapProfile(mf); err != nil {
		logger.Fatalf("Error writing memory profile: %v", err)
	}
	hist := stats.NewHistogram(hopts)
	for _, h := range hists {
		hist.Merge(h)
	}
	parseHist(hist)
	fmt.Println("Client CPU utilization:", cpu)
	fmt.Println("Client CPU profile:", cf.Name())
	fmt.Println("Client Mem Profile:", mf.Name())
}

func buildConnections(ctx context.Context) []*grpc.ClientConn {
	ccs := make([]*grpc.ClientConn, *numConn)
	for i := range ccs {
		ccs[i] = benchmark.NewClientConnWithContext(ctx, "localhost:"+*port,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithWriteBufferSize(128*1024),
			grpc.WithReadBufferSize(128*1024),
		)
	}
	return ccs
}

func runWithConn(cc *grpc.ClientConn, pool *keyPool, warmDeadline, endDeadline time.Time) {
	for i := 0; i < *numRPC; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			caller := makeCaller(cc, pool)
			hist := stats.NewHistogram(hopts)
			for {
				start := time.Now()
				if start.After(endDeadline) {
					mu.Lock()
					hists = append(hists, hist)
					mu.Unlock()
					return
				}
				caller()
				elapsed := time.Since(start)
				if start.After(warmDeadline) {
					_ = hist.Add(elapsed.Nanoseconds())
				}
			}
		}()
	}
}

// selectOperation returns the operation type based on request type and distribution
// For mixed mode: 70% Get, 30% Set
func selectOperation() string {
	if *reqType != "mixed" {
		return *reqType
	}

	r := rand.IntN(100)
	if r < 70 { // 70% Get
		return "get"
	}
	return "set" // 30% Set
}

func makeCaller(cc *grpc.ClientConn, pool *keyPool) func() {
	client := lumepb.NewLumeClient(cc)

	if *rpcType == "unary" {
		return func() {
			key := pool.randomKey()
			op := selectOperation()

			switch op {
			case "get":
				_, err := client.Get(context.Background(), &lumepb.GetRequest{
					Key: key,
				})
				if err != nil {
					// Non-fatal errors during benchmarking
					return
				}
			case "set":
				// Create new key with LWW-Register
				_, err := client.Set(context.Background(), &lumepb.SetRequest{
					Key:      key,
					CrdtType: lumepb.Type_TYPE_LWW_REGISTER,
				})
				if err != nil {
					return
				}
			}
		}
	}

	// Streaming mode - use LumeStreaming service
	// Note: Lume streaming API is server-streaming (one request -> stream of responses)
	// So we need to create new streams for each "request" to match the benchmark pattern
	streamingClient := lumepb.NewLumeStreamingClient(cc)

	return func() {
		key := pool.randomKey()
		op := selectOperation()

		switch op {
		case "get":
			// Create Get stream and read one response
			getStream, err := streamingClient.Get(context.Background(), &lumepb.GetRequest{
				Key: key,
			})
			if err != nil {
				return
			}
			_, err = getStream.Recv()
			if err != nil {
				return
			}
		case "set":
			// Create Set stream and read one response
			setStream, err := streamingClient.Set(context.Background(), &lumepb.SetRequest{
				Key:      key,
				CrdtType: lumepb.Type_TYPE_LWW_REGISTER,
			})
			if err != nil {
				return
			}
			_, err = setStream.Recv()
			if err != nil {
				return
			}
		}
	}
}

func parseHist(hist *stats.Histogram) {
	fmt.Println("qps:", float64(hist.Count)/float64(*duration))
	fmt.Printf("Latency: (50/90/99 %%ile): %v/%v/%v\n",
		time.Duration(median(.5, hist)),
		time.Duration(median(.9, hist)),
		time.Duration(median(.99, hist)))
}

func median(percentile float64, h *stats.Histogram) int64 {
	need := int64(float64(h.Count) * percentile)
	have := int64(0)
	for _, bucket := range h.Buckets {
		count := bucket.Count
		if have+count >= need {
			percent := float64(need-have) / float64(count)
			return int64((1.0-percent)*bucket.LowBound + percent*bucket.LowBound*(1.0+hopts.GrowthFactor))
		}
		have += bucket.Count
	}
	panic("should have found a bound")
}
