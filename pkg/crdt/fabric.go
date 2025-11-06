package crdt

import (
	"fmt"

	"github.com/google/uuid"
)

var ErrCRDTNotFound = fmt.Errorf("crdt not found")

var constructors = map[string]CRDTConstructor{
	PNCounterName: func(id uuid.UUID) CRDT {
		return NewPNCounter[uuid.UUID, int64](id)
	},
}

type fabric struct {
}

func NewFabric() CRDTFabric {
	return &fabric{}
}

func (f *fabric) New(name string, id uuid.UUID) (CRDT, error) {
	constructor, ok := constructors[name]
	if !ok {
		return nil, ErrCRDTNotFound
	}
	return constructor(id), nil
}
