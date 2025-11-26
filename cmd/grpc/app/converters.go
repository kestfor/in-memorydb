package app

import (
	"encoding/json"
	"errors"
	"in-memorydb/api/lumepb"
	"in-memorydb/pkg/crdt"
)

var ErrUnsupportedType = errors.New("unsupported CRDT type")

var toDomainTypeMapping = map[lumepb.Type]crdt.CRDTType{
	lumepb.Type_TYPE_PN_COUNTER:   crdt.CRDTTypePNCounter,
	lumepb.Type_TYPE_LWW_REGISTER: crdt.CRDTTypeLWWHLCRegister,
}

var fromDomainTypeMapping = map[crdt.CRDTType]lumepb.Type{
	crdt.CRDTTypePNCounter:      lumepb.Type_TYPE_PN_COUNTER,
	crdt.CRDTTypeLWWHLCRegister: lumepb.Type_TYPE_LWW_REGISTER,
}

func toDomainCRDTType(t lumepb.Type) (crdt.CRDTType, error) {
	domainType, ok := toDomainTypeMapping[t]
	if !ok {
		return "", ErrUnsupportedType
	}
	return domainType, nil
}

func fromDomainCRDTType(t crdt.CRDTType) (lumepb.Type, error) {
	domainType, ok := fromDomainTypeMapping[t]
	if !ok {
		return lumepb.Type_TYPE_NOT_SPECIFIED, ErrUnsupportedType
	}
	return domainType, nil
}

func toGetResponse(val any, typ crdt.CRDTType, ok bool) (*lumepb.GetResponse, error) {
	if !ok {
		return &lumepb.GetResponse{Ok: false, CrdtType: lumepb.Type_TYPE_NOT_SPECIFIED}, nil
	}

	convertedType, err := fromDomainCRDTType(typ)
	if err != nil {
		return nil, err
	}

	switch typ {
	case crdt.CRDTTypePNCounter:
		data := &lumepb.GetResponse_CounterData{CounterData: &lumepb.GetResponse_Counter{Val: val.(int64)}}
		return &lumepb.GetResponse{CrdtType: convertedType, Data: data, Ok: ok}, nil
	case crdt.CRDTTypeLWWHLCRegister:
		data := &lumepb.GetResponse_RegisterData{RegisterData: &lumepb.GetResponse_Register{Val: val.(json.RawMessage)}}
		return &lumepb.GetResponse{CrdtType: convertedType, Data: data, Ok: ok}, nil
	default:
		return nil, ErrUnsupportedType
	}
}
