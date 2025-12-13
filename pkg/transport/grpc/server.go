package grpc

import (
	"context"
	"github.com/kestfor/in-memorydb/pkg/gossip/gossip_buffer"
	buffer "github.com/kestfor/in-memorydb/pkg/storage/updates_buffer"
	"github.com/kestfor/in-memorydb/pkg/storage/version_manager"
	"github.com/kestfor/in-memorydb/pkg/storage/wal"
	"github.com/kestfor/in-memorydb/pkg/structs"
	"github.com/kestfor/in-memorydb/pkg/transport/grpc/transportpb"
	"log/slog"

	"google.golang.org/protobuf/types/known/emptypb"
)

// number of updates that a node can send via one get call
const maxUpdatesNumber = 10000

type updatesServer struct {
	transportpb.UnimplementedUpdatesServer
	vm      version_manager.VersionManager
	buffer  buffer.UpdatesBuffer
	gbuffer *gossip_buffer.GossipBuffer
	wal     wal.WAL
}

func NewUpdatesServer(buffer buffer.UpdatesBuffer, gbuffer *gossip_buffer.GossipBuffer, wal wal.WAL, vm version_manager.VersionManager) *updatesServer {
	return &updatesServer{buffer: buffer, gbuffer: gbuffer, wal: wal, vm: vm}
}

// Get retrieves the updates for the requested version ranges, using both buffer and WAL as data sources.
func (s *updatesServer) Get(ctx context.Context, request *transportpb.GetRequest) (*transportpb.GetResponse, error) {
	missedRanges := request.GetVersions()
	var result [][]byte

	for nodeID, missedRange := range missedRanges {
		if len(result) >= maxUpdatesNumber {
			break
		}

		for _, r := range missedRange.GetRanges() {

			if len(result) >= maxUpdatesNumber {
				break
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			covering := s.buffer.GetCovering(nodeID, structs.Range{Start: r.Start, End: r.End})

			// fallback to wal
			if len(covering) == 0 {
				for seq := r.Start; seq <= r.End && len(result) < maxUpdatesNumber; seq++ {

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

	slog.DebugContext(ctx, "grpc.Get: Successfully sent requested updates", "count", len(result))
	return &transportpb.GetResponse{Updates: result}, nil
}

// Publish receives updates from another peer and updates the local state via versionManager.
// New updates will be added to buffer and WAL.
func (s *updatesServer) Publish(ctx context.Context, request *transportpb.PublishRequest) (*emptypb.Empty, error) {
	domainUpdates, err := toDomainUpdates(request.Updates)
	if err != nil {
		return nil, err
	}

	// saving updates with not zero ttl for epidemic distribution
	s.gbuffer.AddAndDec(domainUpdates...)

	// applying updates
	applied := s.vm.Update(ctx, domainUpdates...)

	// saving applied updates for fast search
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

	slog.DebugContext(ctx, "grpc.Publish: Successfully publish updates", "count", len(applied))
	return &emptypb.Empty{}, nil
}

// GetVersionVector handles the retrieval of the version vector from the version manager and returns it in the response.
func (s *updatesServer) GetVersionVector(ctx context.Context, request *emptypb.Empty) (*transportpb.GetVersionVectorResponse, error) {
	versVect := s.vm.VectorClockContiguous()
	slog.DebugContext(ctx, "grpc.GetVersionVector: Successfully sent version vector", "versionVector", versVect)
	return &transportpb.GetVersionVectorResponse{VectorClock: versVect}, nil
}

//func (s *updatesServer) RestoreSeq(ctx context.Context, request *transportpb.RestoreSeqRequest) (*emptypb.Empty, error) {
//
//}
