package grpc

import (
	"context"
	"encoding/json"
	"in-memorydb/pkg/storage/transport/grpc/transportpb"
	"in-memorydb/pkg/storage/types"
	buffer "in-memorydb/pkg/storage/updates_buffer"
	"in-memorydb/pkg/storage/version_manager"
	"in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type updatesServer struct {
	transportpb.UnimplementedUpdatesServer
	vm     *version_manager.VersionManager
	buffer buffer.UpdatesBuffer
	wal    wal.WAL
}

func RegisterUpdatesService(server *grpc.Server) {
	transportpb.RegisterUpdatesServer(server, &updatesServer{})
}

func NewUpdatesServer(buffer buffer.UpdatesBuffer, wal wal.WAL, vm *version_manager.VersionManager) *updatesServer {
	return &updatesServer{buffer: buffer, wal: wal, vm: vm}
}

func (s *updatesServer) Get(ctx context.Context, request *transportpb.GetRequest) (*transportpb.GetResponse, error) {
	missedRanges := request.GetVersions()
	var result []*transportpb.Update

	for nodeID, missedRange := range missedRanges {
		for _, r := range missedRange.GetRanges() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			covering := s.buffer.GetCovering(nodeID, structs.Range{Start: r.Start, End: r.End})

			// fallback to wal
			if len(covering) == 0 {
				for seq := r.Start; seq <= r.End; seq++ {

					walEntry, err := s.wal.Get(nodeID, seq)

					// fallback for snapshot, currently not supported
					if err != nil {
						slog.ErrorContext(ctx, "Error getting update from WAL", "err", err, "fromNodeID", nodeID, "seq", seq)
						continue
					}

					var upd types.Update
					err = json.Unmarshal(walEntry.Payload, &upd)
					if err != nil {
						slog.ErrorContext(ctx, "Error unmarshalling update", err, "fromNodeID", nodeID, "seq", seq, "walEntry", walEntry)
						continue
					}

					pb, err := fromDomainUpdate(&upd)
					if err != nil {
						slog.ErrorContext(ctx, "Error while converting", "fromNodeID", nodeID, "seq", seq, "walEntry", walEntry, "err", err)
						continue
					}

					result = append(result, pb)

				}
			} else {
				pbs, err := fromDomainUpdates(covering)
				if err != nil {
					slog.ErrorContext(ctx, "Error while converting", "fromNodeID", nodeID, "updates", covering, "err", err)
					continue
				}
				result = append(result, pbs...)
			}
		}
	}

	slog.InfoContext(ctx, "Successfully sent requested updates", "count", len(result))
	return &transportpb.GetResponse{Updates: result}, nil
}

func (s *updatesServer) Publish(ctx context.Context, request *transportpb.PublishRequest) (*emptypb.Empty, error) {
	domainUpdates, err := toDomainUpdates(request.Updates)
	if err != nil {
		return nil, err
	}

	applied := s.vm.Update(domainUpdates...)
	s.buffer.Put(applied...)
	for _, u := range applied {

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		bytes, err := json.Marshal(u)
		if err != nil {
			slog.ErrorContext(ctx, "Error marshalling update, can't add to WAL", "err", err)
			continue
		}
		for seq := u.Range.Start; seq <= u.Range.End; seq++ {
			err := s.wal.Append(&wal.Entry{NodeID: u.NodeID, SeqNum: seq, Payload: bytes})
			if err != nil {
				slog.ErrorContext(ctx, "Error adding update to WAL", "err", err)
				continue
			}
		}
	}

	slog.InfoContext(ctx, "Successfully sent published updates", "count", len(applied))
	return &emptypb.Empty{}, nil
}

func (s *updatesServer) GetVersionVector(ctx context.Context, request *emptypb.Empty) (*transportpb.GetVersionVectorResponse, error) {
	versVect := s.vm.VectorClockContiguous()
	slog.InfoContext(ctx, "Successfully sent version vector", "versionVector", versVect)
	return &transportpb.GetVersionVectorResponse{VectorClock: versVect}, nil
}
