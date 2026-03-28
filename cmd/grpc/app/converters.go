package app

import (
	"encoding/json"
	"errors"
	lume "github.com/kestfor/in-memorydb/api/lume"
	"github.com/kestfor/in-memorydb/pkg/crdt"
)

var ErrUnsupportedType = errors.New("unsupported CRDT type")

var toDomainTypeMapping = map[lume.Type]crdt.CRDTType{
	lume.Type_TYPE_PN_COUNTER:   crdt.CRDTTypePNCounter,
	lume.Type_TYPE_LWW_REGISTER: crdt.CRDTTypeLWWHLCRegister,
}

var fromDomainTypeMapping = map[crdt.CRDTType]lume.Type{
	crdt.CRDTTypePNCounter:      lume.Type_TYPE_PN_COUNTER,
	crdt.CRDTTypeLWWHLCRegister: lume.Type_TYPE_LWW_REGISTER,
}

func toDomainCRDTType(t lume.Type) (crdt.CRDTType, error) {
	domainType, ok := toDomainTypeMapping[t]
	if !ok {
		return "", ErrUnsupportedType
	}
	return domainType, nil
}

func fromDomainCRDTType(t crdt.CRDTType) (lume.Type, error) {
	domainType, ok := fromDomainTypeMapping[t]
	if !ok {
		return lume.Type_TYPE_NOT_SPECIFIED, ErrUnsupportedType
	}
	return domainType, nil
}

func toGetResponse(val any, typ crdt.CRDTType, ok bool) (*lume.GetResponse, error) {
	if !ok {
		return &lume.GetResponse{Ok: false, CrdtType: lume.Type_TYPE_NOT_SPECIFIED}, nil
	}

	convertedType, err := fromDomainCRDTType(typ)
	if err != nil {
		return nil, err
	}

	switch typ {
	case crdt.CRDTTypePNCounter:
		data := &lume.GetResponse_CounterData{CounterData: &lume.GetResponse_Counter{Val: val.(int64)}}
		return &lume.GetResponse{CrdtType: convertedType, Data: data, Ok: ok}, nil
	case crdt.CRDTTypeLWWHLCRegister:
		data := &lume.GetResponse_RegisterData{RegisterData: &lume.GetResponse_Register{Val: val.(json.RawMessage)}}
		return &lume.GetResponse{CrdtType: convertedType, Data: data, Ok: ok}, nil
	default:
		return nil, ErrUnsupportedType
	}
}
