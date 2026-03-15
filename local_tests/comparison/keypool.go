package main

import (
	"github.com/kestfor/in-memorydb/local_tests/comparison/models"
	"golang.org/x/exp/rand"
)

type KeyPool struct {
	keys []string
	objs []*models.User
}

func (p *KeyPool) GetKey() string {
	ind := rand.Intn(len(p.keys))
	return p.keys[ind]
}

func (p *KeyPool) GetObj() *models.User {
	ind := rand.Intn(len(p.objs))
	return p.objs[ind]
}

func (p *KeyPool) Put(key string, obj *models.User) {
	p.keys = append(p.keys, key)
	p.objs = append(p.objs, obj)
}

func NewKeyPool() *KeyPool {
	return &KeyPool{
		keys: make([]string, 0),
		objs: make([]*models.User, 0),
	}
}
