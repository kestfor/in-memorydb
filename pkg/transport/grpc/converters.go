package grpc

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kestfor/in-memorydb/pkg/crdt"
	"github.com/kestfor/in-memorydb/pkg/crdt/hlc"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"github.com/kestfor/in-memorydb/pkg/types"
)

var ErrCannotConvert = errors.New("cannot convert to transport data")

func fromDomainUpdate(update *types.Update) ([]byte, error) {
	jsonPayload, err := json.Marshal(update)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCannotConvert, err)
	}
	return jsonPayload, nil
}

func fromDomainUpdates(updates []*types.Update) ([][]byte, error) {
	result := make([][]byte, 0, len(updates))
	for _, update := range updates {
		u, err := fromDomainUpdate(update)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil
}

func toDomainUpdate(update []byte) (*types.Update, error) {

	var upd types.Update
	if err := json.Unmarshal(update, &upd); err != nil {
		return nil, err
	}

	return &upd, nil
}

func fromDomainVersions(versions map[string][]structs.Range) map[string]*transportpb.RangeList {
	result := make(map[string]*transportpb.RangeList, len(versions))
	for k, v := range versions {

		arr := make([]*transportpb.Range, 0, len(v))
		for _, r := range v {
			arr = append(arr, &transportpb.Range{
				Start: r.Start,
				End:   r.End,
			})
		}

		result[k] = &transportpb.RangeList{
			Ranges: arr,
		}
	}
	return result
}

func toDomainVersions(versions map[string]*transportpb.RangeList) map[string][]structs.Range {
	result := make(map[string][]structs.Range, len(versions))
	for k, v := range versions {
		l := make([]structs.Range, 0, len(v.GetRanges()))
		for _, r := range v.GetRanges() {
			l = append(l, structs.Range{
				Start: r.Start,
				End:   r.End,
			})
		}
		result[k] = l
	}
	return result
}

func toDomainUpdates(updates [][]byte) ([]*types.Update, error) {
	result := make([]*types.Update, 0, len(updates))
	for _, update := range updates {
		u, err := toDomainUpdate(update)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil
}

func fromKeyState(ks *types.KeyState) *transportpb.KeyStateProto {
	if ks == nil {
		return nil
	}
	pb := &transportpb.KeyStateProto{
		Key:       ks.Key,
		CrdtType:  string(ks.CRDTType),
		State:     ks.State,
		Tombstone: ks.Tombstone,
		Vc:        ks.VC,
	}
	if ks.SetTimeStamp != nil {
		pb.SetTimeStampWall = ks.SetTimeStamp.WallTime
		pb.SetTimeStampLamport = ks.SetTimeStamp.Lamport
		pb.SetTimeStampId = ks.SetTimeStamp.ID
	}
	return pb
}

func toKeyState(pb *transportpb.KeyStateProto) *types.KeyState {
	if pb == nil {
		return nil
	}
	ks := &types.KeyState{
		Key:       pb.Key,
		CRDTType:  crdt.CRDTType(pb.CrdtType),
		State:     pb.State,
		Tombstone: pb.Tombstone,
		VC:        pb.Vc,
	}
	if pb.SetTimeStampWall != 0 || pb.SetTimeStampLamport != 0 || pb.SetTimeStampId != "" {
		ks.SetTimeStamp = &hlc.Timestamp{
			WallTime: pb.SetTimeStampWall,
			Lamport:  pb.SetTimeStampLamport,
			ID:       pb.SetTimeStampId,
		}
	}
	return ks
}

func fromKeyStates(states []*types.KeyState) []*transportpb.KeyStateProto {
	result := make([]*transportpb.KeyStateProto, 0, len(states))
	for _, ks := range states {
		result = append(result, fromKeyState(ks))
	}
	return result
}

func toKeyStates(pbs []*transportpb.KeyStateProto) []*types.KeyState {
	result := make([]*types.KeyState, 0, len(pbs))
	for _, pb := range pbs {
		result = append(result, toKeyState(pb))
	}
	return result
}
