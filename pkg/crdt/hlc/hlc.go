package hlc

import (
	"fmt"
	"sync/atomic"
	"time"
)

const (
	Lower   = -1
	Equal   = 0
	Greater = 1
)

type Timestamp struct {
	WallTime uint64 `json:"wall_time"` // Наносекунды (UnixNano)
	Lamport  uint64 `json:"lamport"`
	ID       string `json:"id"`
}

func (t *Timestamp) Copy() *Timestamp {
	return &Timestamp{
		WallTime: t.WallTime,
		Lamport:  t.Lamport,
		ID:       t.ID,
	}
}

// Time возвращает time.Time из наносекунд
func (t *Timestamp) Time() time.Time {
	return time.Unix(0, int64(t.WallTime))
}

func (t *Timestamp) LamportTime() uint64 {
	return t.Lamport
}

// Equal compares the current Timestamp with another Timestamp and returns true if they are equal in WallTime and Lamport values.
func (t *Timestamp) Equal(another *Timestamp) bool {
	return t.WallTime == another.WallTime && t.Lamport == another.Lamport
}

func (t *Timestamp) Before(other *Timestamp) bool { return Compare(t, other) == Lower }
func (t *Timestamp) After(other *Timestamp) bool  { return Compare(t, other) == Greater }
func (t *Timestamp) String() string {
	return fmt.Sprintf("(%s, L=%d, id=%s)",
		time.Unix(0, int64(t.WallTime)).UTC().Format(time.RFC3339Nano),
		t.Lamport, t.ID)
}

func Compare(a, b *Timestamp) int {
	if a.WallTime < b.WallTime {
		return Lower
	}
	if a.WallTime > b.WallTime {
		return Greater
	}
	if a.Lamport < b.Lamport {
		return Lower
	}
	if a.Lamport > b.Lamport {
		return Greater
	}
	if a.ID < b.ID {
		return Lower
	}
	if a.ID > b.ID {
		return Greater
	}
	return Equal
}

type pair struct {
	wall    uint64
	logical uint64
}

type Time struct {
	nodeID string
	st     atomic.Pointer[pair]
	offset atomic.Int64
}

func NewHLC(nodeID string) *Time {
	t := &Time{
		nodeID: nodeID,
	}
	t.st.Store(&pair{wall: 0, logical: 0})
	return t
}

func (h *Time) WithOffset(offset time.Duration) *Time {
	h.offset.Store(int64(offset))
	return h
}

func (h *Time) nowNano() uint64 {
	off := time.Duration(h.offset.Load())
	return uint64(time.Now().Add(off).UnixNano())
}

func (h *Time) Now() *Timestamp {
	for {
		now := h.nowNano()
		p := h.st.Load()

		lastWall := p.wall
		logical := p.logical

		var newPair pair
		if now > lastWall {
			newPair.wall = now
			newPair.logical = 0
		} else {
			newPair.wall = lastWall
			newPair.logical = logical + 1
		}

		newPtr := &newPair
		if h.st.CompareAndSwap(p, newPtr) {
			return &Timestamp{
				WallTime: newPtr.wall,
				Lamport:  newPtr.logical,
				ID:       h.nodeID,
			}
		}
	}
}

func (h *Time) SyncWithRemote(remote *Timestamp) *Timestamp {
	for {
		now := h.nowNano()
		p := h.st.Load()

		lastWall := p.wall
		logical := p.logical

		newWall := lastWall
		if now > newWall {
			newWall = now
		}
		if remote != nil && remote.WallTime > newWall {
			newWall = remote.WallTime
		}

		var newLogical uint64
		switch {
		case remote != nil && newWall == remote.WallTime && newWall == now:
			if remote.Lamport >= logical {
				newLogical = remote.Lamport + 1
			} else {
				newLogical = logical + 1
			}
		case remote != nil && newWall == remote.WallTime:
			newLogical = remote.Lamport + 1
		case newWall == now && now > lastWall:
			newLogical = 0
		default:
			newLogical = logical + 1
		}

		newPtr := &pair{wall: newWall, logical: newLogical}

		if h.st.CompareAndSwap(p, newPtr) {
			return &Timestamp{
				WallTime: newWall,
				Lamport:  newLogical,
				ID:       h.nodeID,
			}
		}
	}
}
