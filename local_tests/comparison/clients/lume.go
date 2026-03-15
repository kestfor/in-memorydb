package client

import (
	"context"
	"log/slog"
	"time"

	"github.com/kestfor/in-memorydb/api/lume"
	"github.com/kestfor/in-memorydb/local_tests/comparison/models"
	"github.com/kestfor/in-memorydb/local_tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const lumePoolSize = 1

var lumeLabelsSet = prometheus.Labels{"op": "set", "db": "lume"}
var lumeLabelsGet = prometheus.Labels{"op": "get", "db": "lume"}

type LumeClient struct {
	client lume.LumeClient
	m      *monitoring.Metrics
}

func NewLumeClient(url string, m *monitoring.Metrics) *LumeClient {

	conn, err := grpc.NewClient(url,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithWriteBufferSize(128*1024),
		grpc.WithReadBufferSize(128*1024),
	)
	if err != nil {
		slog.Error("grpc.NewClient failed", "err", err)
		panic(err)
	}
	client := lume.NewLumeClient(conn)

	return &LumeClient{client: client, m: m}
}

func (c *LumeClient) Get(ctx context.Context, key string) (models.User, error) {
	now := time.Now()
	_, err := c.client.Get(ctx, &lume.GetRequest{Key: key})
	if err != nil {
		return models.User{}, nil
	}
	c.m.Duration().With(lumeLabelsGet).Observe(time.Since(now).Seconds())
	return models.User{}, nil
}

func (c *LumeClient) Set(ctx context.Context, key string, value *models.User) error {
	_, err := json.Marshal(value)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = c.client.Set(ctx, &lume.SetRequest{Key: key, CrdtType: lume.Type_TYPE_LWW_REGISTER})
	if err != nil {
		return nil
	}
	c.m.Duration().With(lumeLabelsSet).Observe(time.Since(now).Seconds())
	return nil
}
