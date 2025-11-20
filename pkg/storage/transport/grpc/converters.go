package grpc

import (
	"encoding/json"
	"errors"
	"in-memorydb/pkg/crdt"
	"in-memorydb/pkg/storage"
	"in-memorydb/pkg/storage/transport/grpc/transportpb"
	"in-memorydb/pkg/structs"
)

var fabr = crdt.NewFabric()

func fromDomainType(updateType storage.UpdateType) transportpb.UpdateType {
	switch updateType {
	case storage.UpdateTypeDelete:
		return transportpb.UpdateType_UPDATE_TYPE_DELETE
	case storage.UpdateTypeSet:
		return transportpb.UpdateType_UPDATE_TYPE_SET
	case storage.UpdateTypeDelta:
		return transportpb.UpdateType_UPDATE_TYPE_DELTA
	default:
		return transportpb.UpdateType_UPDATE_TYPE_UNSPECIFIED
	}
}

func toDomainType(updateType transportpb.UpdateType) (storage.UpdateType, error) {
	switch updateType {
	case transportpb.UpdateType_UPDATE_TYPE_DELETE:
		return storage.UpdateTypeDelete, nil
	case transportpb.UpdateType_UPDATE_TYPE_SET:
		return storage.UpdateTypeSet, nil
	case transportpb.UpdateType_UPDATE_TYPE_DELTA:
		return storage.UpdateTypeDelta, nil
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

func fromDomainUpdate(update *storage.Update) (*transportpb.Update, error) {
	jsonPayload, err := json.Marshal(update)
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

func fromDomainUpdates(updates []*storage.Update) ([]*transportpb.Update, error) {
	result := make([]*transportpb.Update, len(updates))
	for _, update := range updates {
		u, err := fromDomainUpdate(update)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil
}

func toDomainUpdate(update *transportpb.Update) (*storage.Update, error) {

	delta, err := fabr.DeltaFromBytes(update.CrdtType, update.Payload)
	if err != nil {
		return nil, err
	}

	typ, err := toDomainType(update.Type)
	if err != nil {
		return nil, err
	}

	return &storage.Update{
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

func fromDomainVersion(version storage.Version) map[string]*transportpb.Range {
	result := make(map[string]*transportpb.Range, len(version))
	for k, v := range version {
		result[k] = &transportpb.Range{
			Start: v.Start,
			End:   v.End,
		}
	}
	return result
}

func toDomainVersion(version map[string]*transportpb.Range) storage.Version {
	result := make(storage.Version, len(version))
	for k, v := range version {
		result[k] = structs.Range{Start: v.Start, End: v.End}
	}
	return result
}

func toDomainUpdates(updates []*transportpb.Update) ([]*storage.Update, error) {
	result := make([]*storage.Update, len(updates))
	for _, update := range updates {
		u, err := toDomainUpdate(update)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, nil

}
