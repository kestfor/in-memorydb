package grpc

import (
	"encoding/json"
	"errors"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage/transport/grpc/transportpb"
	"in-memorydb/pkg/storage/types"
	"in-memorydb/pkg/structs"
)

var fabr = crdt.NewFabric()

func fromDomainType(updateType types.UpdateType) transportpb.UpdateType {
	switch updateType {
	case types.UpdateTypeDelete:
		return transportpb.UpdateType_UPDATE_TYPE_DELETE
	case types.UpdateTypeSet:
		return transportpb.UpdateType_UPDATE_TYPE_SET
	case types.UpdateTypeDelta:
		return transportpb.UpdateType_UPDATE_TYPE_DELTA
	default:
		return transportpb.UpdateType_UPDATE_TYPE_UNSPECIFIED
	}
}

func toDomainType(updateType transportpb.UpdateType) (types.UpdateType, error) {
	switch updateType {
	case transportpb.UpdateType_UPDATE_TYPE_DELETE:
		return types.UpdateTypeDelete, nil
	case transportpb.UpdateType_UPDATE_TYPE_SET:
		return types.UpdateTypeSet, nil
	case transportpb.UpdateType_UPDATE_TYPE_DELTA:
		return types.UpdateTypeDelta, nil
	default:
		return "", errors.New("unknown update type")
	}
}

func fromDomainTimeStamp(timeStamp *crdt.Timestamp) *transportpb.TimeStamp {
	return &transportpb.TimeStamp{
		Lamport:  timeStamp.Lamport,
		Id:       timeStamp.ID,
		WallTime: timeStamp.WallTime,
	}
}

func toDomainTimeStamp(timeStamp *transportpb.TimeStamp) *crdt.Timestamp {
	return &crdt.Timestamp{
		Lamport:  timeStamp.Lamport,
		WallTime: timeStamp.WallTime,
		ID:       timeStamp.Id,
	}
}

func fromDomainUpdate(update *types.Update) (*transportpb.Update, error) {
	jsonPayload, err := json.Marshal(update.Payload)
	if err != nil {
		return nil, err
	}

	return &transportpb.Update{
		NodeId: update.NodeID,
		Key:    update.Key,
		Range: &transportpb.Range{
			Start: update.Range.Start,
			End:   update.Range.End,
		},
		CrdtType: update.Payload.Type().String(),
		Ts:       fromDomainTimeStamp(update.TimeStamp),
		Payload:  jsonPayload,
		Type:     fromDomainType(update.Type),
	}, nil
}

func fromDomainUpdates(updates []*types.Update) ([]*transportpb.Update, error) {
	result := make([]*transportpb.Update, 0, len(updates))
	for _, update := range updates {
		u, err := fromDomainUpdate(update)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil
}

func toDomainUpdate(update *transportpb.Update) (*types.Update, error) {

	delta, err := fabr.DeltaFromBytes(crdt.CRDTType(update.CrdtType), update.Payload)
	if err != nil {
		return nil, err
	}

	typ, err := toDomainType(update.Type)
	if err != nil {
		return nil, err
	}

	return &types.Update{
		NodeID: update.NodeId,
		Key:    update.Key,
		Range: structs.Range{
			Start: update.Range.Start,
			End:   update.Range.End,
		},
		TimeStamp: toDomainTimeStamp(update.Ts),
		Payload:   delta,
		Type:      typ,
	}, nil
}

func fromDomainVersions(versions map[string][]structs.Range) map[string]*transportpb.RangeList {
	result := make(map[string]*transportpb.RangeList, len(versions))
	for k, v := range versions {

		arr := make([]*transportpb.Range, len(v))
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

func toDomainUpdates(updates []*transportpb.Update) ([]*types.Update, error) {
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
