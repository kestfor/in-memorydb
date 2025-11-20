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
		var counter LWWHLCRegisterDelta
		err := json.Unmarshal(bytes, &counter)
		if err != nil {
			return nil, err
		}
		return &counter, nil
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

func (f *fabric) DeltaFromBytes(typeName string, bytes []byte) (Delta, error) {

	fromBytesConstr, ok := fromBytesConstructors[CRDTType(typeName)]
	if !ok {
		return nil, ErrCRDTNotFound
	}
	return fromBytesConstr(bytes)
}
