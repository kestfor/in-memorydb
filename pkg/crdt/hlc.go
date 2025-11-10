package crdt

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Результаты сравнения
const (
	Lower   = -1
	Equal   = 0
	Greater = 1
)

// Timestamp — публичная временная метка HLC.
// WallTime в наносекундах (UnixNano).
type Timestamp struct {
	WallTime uint64 `json:"wall_time"`
	Lamport  uint64 `json:"lamport"`
	ID       string `json:"id"`
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

// приватная структура состояния, на которую будем держать atomic.Pointer
type pair struct {
	wall    uint64
	logical uint64
}

// Time — HLC генератор без блокировок (использует atomic.Pointer на pair)
type Time struct {
	nodeID string
	st     atomic.Pointer[pair] // указывает на текущее (wall, logical)
	offset atomic.Int64         // смещение в наносекундах (для тестов/симуляции)
}

// NewHLC создаёт HLC-генератор для указанного nodeID.
func NewHLC(nodeID string) *Time {
	t := &Time{
		nodeID: nodeID,
	}
	// инициализируем состояние (0,0)
	t.st.Store(&pair{wall: 0, logical: 0})
	return t
}

// WithOffset задаёт смещение системного времени (для симуляций / тестов).
// Принимает time.Duration (можно передать 0).
func (h *Time) WithOffset(offset time.Duration) *Time {
	h.offset.Store(int64(offset))
	return h
}

func (h *Time) nowNano() uint64 {
	off := time.Duration(h.offset.Load())
	return uint64(time.Now().Add(off).UnixNano())
}

// Now генерирует локальную метку.
// Алгоритм:
//   - читаем текущее состояние p
//   - вычисляем новое состояние newP в соответствии с HLC rules
//   - пытаемся CAS заменить p -> newP
//   - при неудаче повторяем
func (h *Time) Now() *Timestamp {
	for {
		now := h.nowNano()
		p := h.st.Load() // *pair (snapshot)

		lastWall := p.wall
		logical := p.logical

		var newPair pair
		if now > lastWall {
			newPair.wall = now
			newPair.logical = 0
		} else {
			// now <= lastWall
			newPair.wall = lastWall
			newPair.logical = logical + 1
		}

		// CAS: заменяем старый указатель на новый (новая структура)
		newPtr := &newPair
		if h.st.CompareAndSwap(p, newPtr) {
			return &Timestamp{WallTime: newPtr.wall, Lamport: newPtr.logical, ID: h.nodeID}
		}
		// иначе кто-то другой изменил состояние — повторяем
	}
}

// SyncWithRemote сливает удалённую метку remote и возвращает новую локальную метку.
// remote может быть nil — тогда поведение эквивалентно Now() с учётом now.
func (h *Time) SyncWithRemote(remote *Timestamp) *Timestamp {
	for {
		now := h.nowNano()
		p := h.st.Load()

		lastWall := p.wall
		logical := p.logical

		// вычисляем новый wall = max(lastWall, now, remote.WallTime)
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
			// если равны все три — берём max(localLogical, remote.Lamport) + 1
			if remote.Lamport >= logical {
				newLogical = remote.Lamport + 1
			} else {
				newLogical = logical + 1
			}
		case remote != nil && newWall == remote.WallTime:
			// remote выиграл -> remote.Lamport + 1
			newLogical = remote.Lamport + 1
		case newWall == now && now > lastWall:
			// локальное физическое время продвинулось -> logical = 0
			newLogical = 0
		default:
			// старый локальный wall остался максимальным -> logical++
			newLogical = logical + 1
		}

		newPtr := &pair{wall: newWall, logical: newLogical}

		if h.st.CompareAndSwap(p, newPtr) {
			return &Timestamp{WallTime: newWall, Lamport: newLogical, ID: h.nodeID}
		}
		// повторяем при неудачном CAS
	}
}
