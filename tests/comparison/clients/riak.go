package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/basho/riak-go-client"
	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

var riakLabelsSet = prometheus.Labels{"op": "set", "db": "riak"}
var riakLabelsGet = prometheus.Labels{"op": "get", "db": "riak"}

const defaultBucket = "users"

type RiakClient struct {
	client *riak.Client
	m      *monitoring.Metrics
	bucket string
}

func NewRiakClient(addr string, m *monitoring.Metrics) *RiakClient {
	client, err := riak.NewClient(&riak.NewClientOptions{
		RemoteAddresses: []string{addr},
	})
	if err != nil {
		panic(fmt.Errorf("riak.NewClient: %w", err))
	}

	return &RiakClient{
		client: client,
		m:      m,
		bucket: defaultBucket,
	}
}

func (c *RiakClient) Close() error {
	return c.client.Stop()
}

func (c *RiakClient) Get(ctx context.Context, key string) (models.User, error) {
	now := time.Now()

	cmd, err := riak.NewFetchValueCommandBuilder().
		WithBucket(c.bucket).
		WithKey(key).
		WithR(1).
		Build()
	if err != nil {
		return models.User{}, err
	}

	if err := c.client.Execute(cmd); err != nil {
		slog.Error("riak.Get failed", "err", err)
		return models.User{}, err
	}

	fetchCmd := cmd.(*riak.FetchValueCommand)

	if fetchCmd.Response == nil || fetchCmd.Response.IsNotFound {
		return models.User{}, fmt.Errorf("key not found")
	}

	if len(fetchCmd.Response.Values) == 0 {
		return models.User{}, fmt.Errorf("empty response")
	}

	//var user models.User
	//if err := json.Unmarshal(fetchCmd.Response.Values[0].Value, &user); err != nil {
	//	return models.User{}, err
	//}

	c.m.Duration().With(riakLabelsGet).Observe(time.Since(now).Seconds())
	return models.User{}, nil
}

func (c *RiakClient) Set(ctx context.Context, key string, value *models.User) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	now := time.Now()

	obj := &riak.Object{
		ContentType: "application/json",
		Value:       b,
	}

	cmd, err := riak.NewStoreValueCommandBuilder().
		WithBucket(c.bucket).
		WithKey(key).
		WithContent(obj).
		WithW(1).
		WithDw(0).
		Build()
	if err != nil {
		return err
	}

	if err := c.client.Execute(cmd); err != nil {
		slog.Error("riak.Set failed", "err", err)
		return err
	}

	c.m.Duration().With(riakLabelsSet).Observe(time.Since(now).Seconds())
	return nil
}
