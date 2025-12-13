package main

import (
	"context"
	"flag"
	"github/kestfor/in-memorydb/cmd/grpc/app"
)

func main() {
	res := flag.String("config", "", "path/to/config.yaml")
	flag.Parse()

	ctx := context.Background()
	app.Run(ctx, res)
}
