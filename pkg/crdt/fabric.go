package crdt

import (
	"fmt"
)

var ErrCRDTNotFound = fmt.Errorf("crdt not found")

var constructors = map[CRDTType]CRDTConstructor{
	CRDTTypePNCounter: func(id string) CRDT {
		return NewPNCounter(id)
	},
	CRDTTypeLWWHLCRegister: func(id string) CRDT {
		return NewLWWHLCRegister(id)
	},
}

type fabric struct {
}

func NewFabric() CRDTFabric {
	return &fabric{}
}

func (f *fabric) New(name CRDTType, id string) (CRDT, error) {
	constructor, ok := constructors[name]
	if !ok {
		return nil, ErrCRDTNotFound
	}
	return constructor(id), nil
}
