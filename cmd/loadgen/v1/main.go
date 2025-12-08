package main

import (
	"fmt"
	lt "in-memorydb/local_tests/loadtests"
	"log"
	"os"
	"time"
)

func main() {
	cfg := lt.LoadConfig{
		TargetAddr:     "127.0.0.1:50051",
		Duration:       1 * time.Minute,
		Concurrency:    100,
		RateLimitRPS:   10000,
		PayloadSize:    256,
		CounterStep:    1,
		MixedSetPct:    20,
		MixedGetPct:    40,
		MixedApplyPct:  30,
		MixedDeletePct: 10,
	}

	stages := []string{
		lt.TestSet,
		lt.TestApply,
		lt.TestGet,
		lt.TestMixed,
	}

	for _, stage := range stages {
		cfg.Type = stage
		fmt.Printf("\n=== Running stage: %v ===\n", stage)

		m := lt.RunLoadTest(cfg)

		fmt.Println("Total:", m.Total)
		fmt.Println("Success:", m.Success)
		fmt.Println("Failed:", m.Failed)
		fmt.Println("p50:", m.Percentile(0.50))
		fmt.Println("p95:", m.Percentile(0.95))
		fmt.Println("p99:", m.Percentile(0.99))

		f, err := os.OpenFile(fmt.Sprintf("./stage_%s.json", stage), os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0755)
		if err != nil {
			log.Println(err)
			continue
		}

		err = lt.SaveMetricsJSON(m, f)
		if err != nil {
			log.Println(err)
		}
	}
}
