package main

import (
	"fmt"
	"in-memorydb/pkg/config"
	"in-memorydb/pkg/util/logging"
	"log/slog"
)

func main() {
	cfg, err := config.Read("cmd/config.yaml")
	if err != nil {
		panic(err)
	}

	logging.InitDefault(cfg.Node.ID)
	slog.Debug("hello world from node")

	fmt.Println(cfg)
}
