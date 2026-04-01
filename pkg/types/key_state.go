package types

import (
	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
)

type KeyState struct {
	Key          string            `json:"key"`
	CRDTType     crdt.CRDTType     `json:"crdt_type"`
	State        []byte            `json:"state"`
	Tombstone    bool              `json:"tombstone"`
	SetTimeStamp hlc.Timestamp     `json:"set_time_stamp"`
	VC           map[string]uint64 `json:"vc"`
}
