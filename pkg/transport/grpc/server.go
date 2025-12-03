package grpc

import (
	"context"
	buffer "in-memorydb/pkg/storage/updates_buffer"
	"in-memorydb/pkg/storage/version_manager"
	"in-memorydb/pkg/storage/wal"
	"in-memorydb/pkg/structs"
	transportpb "in-memorydb/pkg/transport/grpc/transportpb"
	"log/slog"

	"google.golang.org/protobuf/types/known/emptypb"
)

type updatesServer struct {
	transportpb.UnimplementedUpdatesServer
	vm     *version_manager.VersionManager
	buffer buffer.UpdatesBuffer
	wal    wal.WAL
}

func NewUpdatesServer(buffer buffer.UpdatesBuffer, wal wal.WAL, vm *version_manager.VersionManager) *updatesServer {
	return &updatesServer{buffer: buffer, wal: wal, vm: vm}
}

// Get retrieves the updates for the requested version ranges, using both buffer and WAL as data sources.
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

					upd, err := s.wal.Get(nodeID, seq)

					// fallback for snapshot, currently not supported
					if err != nil {
						slog.ErrorContext(ctx, "grpc.Get: Error getting update from WAL", "err", err, "fromNodeID", nodeID, "seq", seq)
						continue
					}

					pb, err := fromDomainUpdate(upd)
					if err != nil {
						slog.ErrorContext(ctx, "grpc.Get: Error while converting", "fromNodeID", nodeID, "seq", seq, "walEntry", upd, "err", err)
						continue
					}

					result = append(result, pb)

				}
			} else {
				pbs, err := fromDomainUpdates(covering)
				if err != nil {
					slog.ErrorContext(ctx, "grpc.Get: Error while converting", "fromNodeID", nodeID, "updates", covering, "err", err)
					continue
				}
				result = append(result, pbs...)
			}
		}
	}

	slog.InfoContext(ctx, "grpc.Get: Successfully sent requested updates", "count", len(result))
	return &transportpb.GetResponse{Updates: result}, nil
}

// Publish receives updates from another peer and updates the local state via versionManager.
// New updates will be added to buffer and WAL.
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

		err := s.wal.Append(ctx, u)
		if err != nil {
			slog.ErrorContext(ctx, "grpc.Publish: Error adding update to WAL", "err", err)
			continue
		}
	}

	slog.InfoContext(ctx, "grpc.Publish: Successfully sent published updates", "count", len(applied))
	return &emptypb.Empty{}, nil
}

// GetVersionVector handles the retrieval of the version vector from the version manager and returns it in the response.
func (s *updatesServer) GetVersionVector(ctx context.Context, request *emptypb.Empty) (*transportpb.GetVersionVectorResponse, error) {
	versVect := s.vm.VectorClockContiguous()
	slog.InfoContext(ctx, "grpc.GetVersionVector: Successfully sent version vector", "versionVector", versVect)
	return &transportpb.GetVersionVectorResponse{VectorClock: versVect}, nil
}
