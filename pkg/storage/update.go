package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/structs"
)

//go:generate go-enum --marshal --nocase

var ErrCannotMerge = errors.New("cannot merge updates")

/*
ENUM(
Delta // delta update of crdt type to merge
Set  // set key to new value
Delete // delete key
)
*/
type UpdateType string

type Payload interface {
	json.Marshaler
	json.Unmarshaler
	Merge(other crdt.Delta) error
}

type Update struct {
	NodeID    string          `json:"node_id"`
	Type      UpdateType      `json:"type"`
	TimeStamp *crdt.Timestamp `json:"time_stamp"`
	Range     structs.Range   `json:"range"`
	Key       string          `json:"key"`
	Payload   crdt.Delta      `json:"payload"`
}

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

	if u.Type != new.Type {
		u.Type = new.Type
		u.Payload = new.Payload
	} else {
		err = u.Payload.Merge(new.Payload)
		if err != nil {
			return fmt.Errorf("error merging payloads: %w: %w", err, ErrCannotMerge)
		}
	}

	u.Range = mergedRange
	return nil
}
