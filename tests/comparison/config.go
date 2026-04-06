package main

import (
	"log"

	config "github.com/kestfor/in-memorydb/pkg/configx/v2"
	"github.com/kestfor/in-memorydb/tests/comparison/monitoring"
)

type Config struct {
	MetricsConfig monitoring.Config `yaml:"metrics"`
	Databases     []DBConfig        `yaml:"databases"`
	Test          Test              `yaml:"test"`
}

type DBConfig struct {
	Name string `yaml:"name"`
	Host string `yaml:"host"`
}

type Test struct {
	DB             DBConfig `yaml:"-"`
	Name           string   `yaml:"name"`
	Type           string   `yaml:"type"`
	MinClients     int      `yaml:"minClients"`
	ClientsStep    int      `yaml:"clientsStep"`
	MaxClients     int      `yaml:"maxClients"`
	StageIntervalS int      `yaml:"stageIntervalS"`
	RequestDelayMs int      `yaml:"requestDelayMs"`
	MaxKeysNum     int      `yaml:"maxKeysNum"`
}

func (c *Config) loadConfig(path string) {
	err := config.Load(path, c)
	if err != nil {
		log.Fatalf("failed to load config: %s", err)
	}
}
