package v1

import "time"

type EngineConfig struct {
	InitialShards   int           `yaml:"initial_shards" env:"ENGINE_INITIAL_SHARDS" default:"256"`
	DeleteThreshold time.Duration `yaml:"delete_threshold" env:"ENGINE_DELETE_THRESHOLD" default:"1m"`
}

func NewEngineFromConfig(nodeID string, cfg EngineConfig) *Engine {
	opts := []Option{
		WithNodeID(nodeID),
	}
	if cfg.InitialShards > 0 {
		opts = append(opts, WithInitialShards(cfg.InitialShards))
	}
	if cfg.DeleteThreshold > 0 {
		opts = append(opts, WithDeleteThreshold(cfg.DeleteThreshold))
	}
	return NewEngine(opts...).(*Engine)
}
