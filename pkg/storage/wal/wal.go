package wal

type Entry struct {
	NodeID  string
	SeqNum  uint64
	Payload []byte
}

type WAL interface {
	Append(u *Entry) error
	Get(nodeID string, seq uint64) (*Entry, error)
	Replay(nodeID string, fromSeq uint64, fn func(Entry) error) error
	Close() error
}
