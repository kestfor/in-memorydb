package client

import (
	"context"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/kestfor/in-memorydb/local_tests/comparison/models"
	"github.com/kestfor/in-memorydb/local_tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
)

var memcachedLabelsSet = prometheus.Labels{"op": "set", "db": "memcached"}
var memcachedLabelsGet = prometheus.Labels{"op": "get", "db": "memcached"}

type MemcachedClient struct {
	client *memcache.Client
	m      *monitoring.Metrics
}

func NewMemcachedClient(url string, m *monitoring.Metrics) *MemcachedClient {
	client := memcache.New(url)
	client.MaxIdleConns = 500
	client.Timeout = time.Second * 5
	return &MemcachedClient{client: client, m: m}
}

func (c *MemcachedClient) Get(ctx context.Context, key string) (models.User, error) {
	now := time.Now()
	it, err := c.client.Get(key)
	if err != nil {
		return models.User{}, nil
	}
	c.m.Duration().With(memcachedLabelsGet).Observe(time.Since(now).Seconds())

	var user models.User
	if err = json.Unmarshal(it.Value, &user); err != nil {
		return models.User{}, err
	}

	return user, err
}

func (c *MemcachedClient) Set(ctx context.Context, key string, value *models.User) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	now := time.Now()
	err = c.client.Set(&memcache.Item{Key: key, Value: b})
	if err != nil {
		return nil
	}
	c.m.Duration().With(memcachedLabelsSet).Observe(time.Since(now).Seconds())

	return nil
}
