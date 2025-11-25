package buffer

//import (
//	"sync"
//	"time"
//)
//
//type NodeID string
//
//type Block struct {
//	Node     NodeID    // origin node
//	Start    uint64    // inclusive seq
//	End      uint64    // inclusive seq
//	State    []byte    // aggregated state (CRDT state) — компактный
//	Size     int       // approximate bytes in-memory (State + overhead)
//	LastSeen time.Time // for LRU / recency
//}
//
//// per-key entry
//type KeyBuffer struct {
//	Key       string
//	Blocks    []*Block    // sorted by Start, non-overlapping, can have gaps
//	Gaps      IntervalSet // optional, but can be derived from Blocks
//	TotalSize int         // bytes sum of Blocks
//	lock      sync.RWMutex
//}
//
//// global buffer manager
//type BufferManager struct {
//	Keys      map[string]*KeyBuffer
//	TotalSize int  // bytes used across all keys
//	MaxSize   int  // memory cap
//	EvictList *LRU // tracks KeyBuffer recency or blocks
//	lock      sync.Mutex
//}
