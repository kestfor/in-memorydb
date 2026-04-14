package client

import (
	"context"
	"log/slog"
	"time"

	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	client *redis.Client
	m      *monitoring.Metrics
	dbName string
}

func NewRedisCompatibleClient(dbName string, url string, m *monitoring.Metrics) *RedisClient {
	client := redis.NewClient(&redis.Options{
		Addr:               url,
		Password:           "",
		DB:                 0,
		PoolSize:           10000,
		MaxConcurrentDials: 10000,
		DialTimeout:        time.Second * 5,
		ReadTimeout:        time.Second * 5,
		WriteTimeout:       time.Second * 5,
	})

	return &RedisClient{client: client, m: m, dbName: dbName}
}

func (c *RedisClient) labels(op string) prometheus.Labels {
	return prometheus.Labels{"op": op, "db": c.dbName}
}

func (c *RedisClient) Get(ctx context.Context, key string) (models.User, error) {
	now := time.Now()

	var user models.User
	_, err := c.client.Get(ctx, key).Result()
	if err != nil {
		slog.Error("rdb.Get failed", "err", err)
		return models.User{}, err
	}

	//if err = json.Unmarshal([]byte(data), &user); err != nil {
	//	return User{}, annotate(err, "json.Unmarshal failed")
	//}

	c.m.Duration().With(c.labels("get")).Observe(time.Since(now).Seconds())
	return user, err
}

func (c *RedisClient) Set(ctx context.Context, key string, value *models.User) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}

	now := time.Now()
	err = c.client.Set(ctx, key, b, 0).Err()
	if err != nil {
		slog.Error("rdb.Set failed", "err", err)
		return err
	}
	c.m.Duration().With(c.labels("set")).Observe(time.Since(now).Seconds())
	return nil
}
