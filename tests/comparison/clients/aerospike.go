package client

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	aerospike "github.com/aerospike/aerospike-client-go/v8"
	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	aerospikeNamespace = "test"
	aerospikeSet       = "users"
	aerospikeBin       = "payload"
)

var aerospikeLabelsSet = prometheus.Labels{"op": "set", "db": "aerospike"}
var aerospikeLabelsGet = prometheus.Labels{"op": "get", "db": "aerospike"}

type AerospikeClient struct {
	client *aerospike.Client
	m      *monitoring.Metrics
}

func NewAerospikeClient(addr string, m *monitoring.Metrics) *AerospikeClient {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(fmt.Errorf("split aerospike address: %w", err))
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Errorf("parse aerospike port: %w", err))
	}

	policy := aerospike.NewClientPolicy()
	policy.Timeout = 5 * time.Second
	policy.IdleTimeout = 5 * time.Second
	policy.ConnectionQueueSize = 10000

	client, err := aerospike.NewClientWithPolicy(policy, host, portNum)
	if err != nil {
		panic(fmt.Errorf("connect aerospike: %w", err))
	}

	return &AerospikeClient{client: client, m: m}
}

func (c *AerospikeClient) Get(ctx context.Context, key string) (models.User, error) {
	_ = ctx

	asKey, err := aerospike.NewKey(aerospikeNamespace, aerospikeSet, key)
	if err != nil {
		return models.User{}, err
	}

	now := time.Now()
	record, err := c.client.Get(nil, asKey, aerospikeBin)
	if err != nil {
		return models.User{}, err
	}

	c.m.Duration().With(aerospikeLabelsGet).Observe(time.Since(now).Seconds())

	if record == nil {
		return models.User{}, fmt.Errorf("record not found")
	}

	return models.User{}, nil
}

func (c *AerospikeClient) Set(ctx context.Context, key string, value *models.User) error {
	_ = ctx

	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	asKey, err := aerospike.NewKey(aerospikeNamespace, aerospikeSet, key)
	if err != nil {
		return err
	}

	bins := aerospike.BinMap{
		aerospikeBin: b,
	}

	writePolicy := aerospike.NewWritePolicy(0, 0)

	now := time.Now()
	if err := c.client.Put(writePolicy, asKey, bins); err != nil {
		return err
	}

	c.m.Duration().With(aerospikeLabelsSet).Observe(time.Since(now).Seconds())
	return nil
}
