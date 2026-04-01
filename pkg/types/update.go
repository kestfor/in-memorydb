package types

import (
	stdlibjson "encoding/json"
	"errors"
	"fmt"

	jsoniter "github.com/json-iterator/go"
	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
)

//go:generate go-enum --marshal --nocase

var ErrCannotUnmarshal = errors.New("cannot unmarshal update")
var ErrCannotMerge = errors.New("cannot merge updates")

/*
ENUM(
Delta // delta update of crdt type to merge
Set  // set key to new value
Delete // delete key
)
*/
type UpdateType string

var fabric = crdt.NewFabric()

type Payload interface {
	stdlibjson.Marshaler
	stdlibjson.Unmarshaler
	Merge(other crdt.Delta) error
}

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type Update struct {
	Seq          uint64        `json:"seq"`
	TimeStamp    hlc.Timestamp `json:"time_stamp"`     // timestamp of current update
	SetTimeStamp hlc.Timestamp `json:"set_time_stamp"` // oldest update's timestamp, associated with same key and type
	Payload      crdt.Delta    `json:"payload,omitempty"`
	Type         UpdateType    `json:"type"`
	TTL          uint8         `json:"ttl"`
	Key          string        `json:"key"`
	NodeID       string        `json:"node_id"`
}

// types for marshaling data
type alias Update
type wrapped struct {
	CRDTType crdt.CRDTType `json:"crdt_type"`
	*alias
}

func (u *Update) UnmarshalJSON(b []byte) error {
	type aux struct {
		CRDTType crdt.CRDTType `json:"crdt_type"`
	}

	var t aux
	err := json.Unmarshal(b, &t)
	if err != nil {
		return fmt.Errorf("%w: %w", err, ErrCannotUnmarshal)
	}

	u.Payload, err = fabric.NilDelta(t.CRDTType)
	if err != nil {
		return fmt.Errorf("%w: %w", err, ErrCannotUnmarshal)
	}

	return json.Unmarshal(b, (*alias)(u))
}

func (u Update) MarshalJSON() ([]byte, error) {

	if u.Payload == nil {
		return nil, fmt.Errorf("cannot marshal update with nil payload, use typed nil instead")
	}

	wrapper := wrapped{
		alias:    (*alias)(&u),
		CRDTType: u.Payload.Type(),
	}
	return json.Marshal(wrapper)
}

// TODO set -> delta transition
func (u *Update) Merge(new Update) error {
	if u.Key != new.Key {
		return fmt.Errorf("cannot merge updates with different keys: %v and %v: %w", u.Key, new.Key, ErrCannotMerge)
	}

	if u.NodeID != new.NodeID {
		return fmt.Errorf("cannot merge updates with different node ID: %v and %v: %w", u.NodeID, new.NodeID, ErrCannotMerge)
	}

	// TODO посмотреть почему может быть тут ошибка, возникает при
	// lume-bench -r 500 -c 1 -d 30 -test_name test -port 50053 -request_type=set
	//mergedRange, err := u.Range.Merge(new.Range)
	//if err != nil {
	//	return fmt.Errorf("%w: %w", err, ErrCannotMerge)
	//}

	if u.Type == UpdateTypeSet || u.Type == UpdateTypeDelete {
		u.Type = new.Type
		u.Payload = new.Payload
	} else {
		// мб нежелательное share так как в wal уже лежит этот update
		err := u.Payload.Merge(new.Payload)
		if err != nil {
			return fmt.Errorf("error merging payloads: %w: %w", err, ErrCannotMerge)
		}
	}

	u.Seq = new.Seq
	u.TimeStamp = new.TimeStamp

	return nil
}
