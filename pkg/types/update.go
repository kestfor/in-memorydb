package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/crdt/hlc"
	"in-memorydb/pkg/structs"
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
	json.Marshaler
	json.Unmarshaler
	Merge(other crdt.Delta) error
}

type Update struct {
	NodeID       string         `json:"node_id"`
	Type         UpdateType     `json:"type"`
	TimeStamp    *hlc.Timestamp `json:"time_stamp"`     // timestamp of current update
	SetTimeStamp *hlc.Timestamp `json:"set_time_stamp"` // oldest update's timestamp, associated with same key and type
	Range        structs.Range  `json:"range"`
	Key          string         `json:"key"`
	Payload      crdt.Delta     `json:"payload,omitempty"`
	TTL          uint8          `json:"ttl"`
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
func (u *Update) Merge(new *Update) error {
	if u.Key != new.Key {
		return fmt.Errorf("cannot merge updates with different keys: %v and %v: %w", u.Key, new.Key, ErrCannotMerge)
	}

	if u.NodeID != new.NodeID {
		return fmt.Errorf("cannot merge updates with different node ID: %v and %v: %w", u.NodeID, new.NodeID, ErrCannotMerge)
	}

	mergedRange, err := u.Range.Merge(new.Range)
	if err != nil {
		return fmt.Errorf("%w: %w", err, ErrCannotMerge)
	}

	if u.Type == UpdateTypeSet || u.Type == UpdateTypeDelete {
		u.Type = new.Type
		u.Payload = new.Payload
	} else {
		err = u.Payload.Merge(new.Payload)
		if err != nil {
			return fmt.Errorf("error merging payloads: %w: %w", err, ErrCannotMerge)
		}
	}

	u.Range = mergedRange
	u.TimeStamp = new.TimeStamp

	return nil
}
