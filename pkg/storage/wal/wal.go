package wal

import (
	"errors"
	"in-memorydb/pkg/storage/types"
)

var ErrNotFound = errors.New("not found")

type WAL interface {
	// Append appends new update to the end of the log
	Append(u *types.Update) error

	// Get method returns update with specified nodeID and seqNum, if not exists - ErrNotFound will be returned
	Get(nodeID string, seq uint64) (*types.Update, error)

	// Replay replays log entries for a specified node starting from a given sequence number and applies a processing function.
	Replay(nodeID string, fromSeq uint64, fn func(update *types.Update) error) error

	// ReplayAll replays all log entries in the write-ahead-log and applies the given processing function to each entry.
	ReplayAll(fn func(update *types.Update) error) error

	// Close gracefully shuts down the write-ahead log, ensuring all resources are released and pending operations are completed.
	// Should be called to flush pending values
	Close() error
}
