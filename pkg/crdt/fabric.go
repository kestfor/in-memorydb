package crdt

import (
	"encoding/json"
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

var fromBytesConstructors = map[CRDTType]func([]byte) (Delta, error){
	CRDTTypePNCounter: func(bytes []byte) (Delta, error) {
		var counter PNCounterDelta
		err := json.Unmarshal(bytes, &counter)
		if err != nil {
			return nil, err
		}
		return &counter, nil
	},

	CRDTTypeLWWHLCRegister: func(bytes []byte) (Delta, error) {
		var reg LWWHLCRegisterDelta
		err := json.Unmarshal(bytes, &reg)
		if err != nil {
			return nil, err
		}
		return &reg, nil
	},
}

var nilDeltaConstr = map[CRDTType]func() Delta{
	CRDTTypePNCounter: func() Delta {
		return &PNCounterDelta{}
	},
	CRDTTypeLWWHLCRegister: func() Delta {
		return &LWWHLCRegisterDelta{}
	},
}

var defaultFabric = NewFabric()

func DefaultFabric() CRDTFabric {
	return defaultFabric
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

func (f *fabric) DeltaFromBytes(typeName CRDTType, bytes []byte) (Delta, error) {

	fromBytesConstr, ok := fromBytesConstructors[typeName]
	if !ok {
		return nil, ErrCRDTNotFound
	}
	return fromBytesConstr(bytes)
}

func (f *fabric) NilDelta(crdtType CRDTType) (Delta, error) {
	constr, ok := nilDeltaConstr[crdtType]
	if !ok {
		return nil, ErrCRDTNotFound
	}
	return constr(), nil
}
