package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"time"

	lume "github.com/kestfor/in-memorydb/api/lume"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	nodeA := flag.String("node-a", "localhost:8081", "address of the write node (host:port)")
	nodeB := flag.String("node-b", "localhost:8082", "address of the read node (host:port)")
	iterations := flag.Int("iterations", 100, "number of write→converge cycles")
	pollInterval := flag.Duration("poll-interval", 10*time.Millisecond, "interval between Get polls on node-b")
	flag.Parse()

	connA, err := grpc.NewClient(*nodeA, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect node-a %s: %v", *nodeA, err)
	}
	defer connA.Close()

	connB, err := grpc.NewClient(*nodeB, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect node-b %s: %v", *nodeB, err)
	}
	defer connB.Close()

	clientA := lume.NewLumeClient(connA)
	clientB := lume.NewLumeClient(connB)

	latencies := make([]time.Duration, 0, *iterations)

	fmt.Printf("convergence test: node-a=%s → node-b=%s  iterations=%d  poll=%s\n\n",
		*nodeA, *nodeB, *iterations, *pollInterval)

	for i := 0; i < *iterations; i++ {
		key := fmt.Sprintf("conv_test_%d", i)
		value := strconv.FormatInt(time.Now().UnixNano(), 10)

		ctx := context.Background()

		// Создаём / обновляем ключ на узле A
		if _, err := clientA.Set(ctx, &lume.SetRequest{
			Key:      key,
			CrdtType: lume.Type_TYPE_LWW_REGISTER,
		}); err != nil {
			log.Printf("[%d] Set key on node-a failed: %v", i, err)
			continue
		}
		if _, err := clientA.Apply(ctx, &lume.ApplyRequest{
			Key: key,
			Operation: &lume.ApplyRequest_RegisterOperation{
				RegisterOperation: &lume.ApplyRequest_Register{
					Value: []byte(value),
				},
			},
		}); err != nil {
			log.Printf("[%d] Apply on node-a failed: %v", i, err)
			continue
		}

		start := time.Now()

		// Поллим узел B до тех пор, пока значение не совпадёт
		for {
			resp, err := clientB.Get(ctx, &lume.GetRequest{Key: key})
			if err == nil && resp.GetOk() {
				if rd, ok := resp.Data.(*lume.GetResponse_RegisterData); ok {
					if string(rd.RegisterData.Val) == value {
						break
					}
				}
			}
			time.Sleep(*pollInterval)
		}

		lat := time.Since(start)
		latencies = append(latencies, lat)

		if (i+1)%10 == 0 {
			fmt.Printf("  iteration %d/%d  last=%s\n", i+1, *iterations, lat)
		}
	}

	if len(latencies) == 0 {
		fmt.Println("no successful iterations")
		return
	}

	fmt.Printf("\n=== convergence results (%d iterations) ===\n", len(latencies))
	fmt.Printf("  mean  = %s\n", mean(latencies))
	fmt.Printf("  p50   = %s\n", percentile(latencies, 0.50))
	fmt.Printf("  p90   = %s\n", percentile(latencies, 0.90))
	fmt.Printf("  p99   = %s\n", percentile(latencies, 0.99))
	fmt.Printf("  min   = %s\n", latencies[0])
	fmt.Printf("  max   = %s\n", latencies[len(latencies)-1])
}

func mean(d []time.Duration) time.Duration {
	var sum int64
	for _, v := range d {
		sum += int64(v)
	}
	return time.Duration(sum / int64(len(d)))
}

func percentile(d []time.Duration, p float64) time.Duration {
	sorted := make([]time.Duration, len(d))
	copy(sorted, d)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}
