package v1

import "time"

type EngineConfig struct {
	ShardsNum       int           `yaml:"shards_num" env:"ENGINE_SHARDS_NUM" default:"256"`
	DeleteThreshold time.Duration `yaml:"delete_threshold" env:"ENGINE_DELETE_THRESHOLD" default:"1m"`
}

func NewEngineFromConfig(nodeID string, cfg EngineConfig) *Engine {
	opts := []Option{
		WithNodeID(nodeID),
	}
	if cfg.ShardsNum > 0 {
		opts = append(opts, WithInitialShards(cfg.ShardsNum))
	}
	if cfg.DeleteThreshold > 0 {
		opts = append(opts, WithDeleteThreshold(cfg.DeleteThreshold))
	}
	return NewEngine(opts...).(*Engine)
}
