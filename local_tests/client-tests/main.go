package main

import (
	"context"
	"in-memorydb/api/lumepb"
	"log/slog"
	"strconv"

	"google.golang.org/grpc"
)

func getConn1() lumepb.LumeClient {
	client, err := grpc.NewClient("127.0.0.1:9090", grpc.WithInsecure())
	if err != nil {
		panic(err)
	}

	conn := lumepb.NewLumeClient(client)
	return conn
}

func getConn2() lumepb.LumeClient {
	client, err := grpc.NewClient("127.0.0.2:9092", grpc.WithInsecure())
	if err != nil {
		panic(err)
	}

	conn := lumepb.NewLumeClient(client)
	return conn
}

func main() {
	client1 := getConn1()
	client2 := getConn2()
	ctx := context.Background()
	for i := 1; i <= 100000; i++ {
		key := "key" + strconv.Itoa(i)
		req := lumepb.SetRequest{Key: key, CrdtType: lumepb.Type_TYPE_PN_COUNTER}
		var err error
		if i%2 == 0 {
			_, err = client1.Set(ctx, &req)
		} else {
			_, err = client2.Set(ctx, &req)
		}
		if err != nil {
			slog.Error("error setting key: %v", err)
		}
	}
}
