package client

import (
	"context"
	"log/slog"

	jsoniter "github.com/json-iterator/go"
	"github.com/kestfor/in-memorydb/tests/comparison/models"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
)

const (
	Redis     = "redis"
	Memcached = "memcached"
	Lume      = "lume"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type Client interface {
	Get(ctx context.Context, key string) (models.User, error)
	Set(ctx context.Context, key string, value *models.User) error
}

func GetClient(db string, url string, m *monitoring.Metrics) Client {
	switch db {
	case Redis:
		return NewRedisClient(url, m)
	case Memcached:
		return NewMemcachedClient(url, m)
	case Lume:
		return NewLumeClient(url, m)
	default:
		slog.Error("unknown db", slog.String("db", db))
		return nil
	}
}
