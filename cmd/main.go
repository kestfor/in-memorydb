package main

import (
	"fmt"
	"in-memorydb/pkg/config"
)

func main() {
	cfg, err := config.Read("cmd/config.yaml")
	if err != nil {
		panic(err)
	}

	fmt.Println(cfg)
}
