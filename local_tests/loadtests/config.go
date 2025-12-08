package loadtest

import "time"

const (
	TestSet   = "set"
	TestApply = "apply"
	TestGet   = "get"
	TestMixed = "mixed"
)

type LoadConfig struct {
	TargetAddr   string        `yaml:"target_addr" default:"localhost:500051"`
	Duration     time.Duration `yaml:"duration" default:"1m" required:"true"`
	Concurrency  int           `yaml:"concurrency" default:"300" required:"true"`
	RateLimitRPS int           `yaml:"rate_limit_rps" default:"0" required:"true"`

	PayloadSize int   `yaml:"payload_size" default:"256" required:"true"`
	CounterStep int64 `yaml:"counter_step" default:"1" required:"true"`

	MixedSetPct    int    `yaml:"mixed_set_pct" default:"20" required:"true"`
	MixedGetPct    int    `yaml:"mixed_get_pct" default:"40" required:"true"`
	MixedApplyPct  int    `yaml:"mixed_apply_pct" default:"30" required:"true"`
	MixedDeletePct int    `yaml:"mixed_delete_pct" default:"10" required:"true"`
	Type           string `yaml:"type" default:"mixed" required:"true"`
}
