package structs

import (
	"fmt"
	"iter"
)

// CircularBuffer — generic циклический буфер.
// behaviour: если overwrite==false, Enqueue возвращает false при заполнении.
// если overwrite==true, Enqueue перезапишет самый старый элемент.
type CircularBuffer[T any] struct {
	buf       []T
	head      int // индекс для чтения (старейший элемент)
	tail      int // индекс для записи (следующее место)
	size      int // текущее количество элементов
	capacity  int
	overwrite bool
}

// New creates a new CircularBuffer with given capacity.
// capacity must be > 0.
func NewCircularBuffer[T any](capacity int, overwrite bool) *CircularBuffer[T] {
	if capacity <= 0 {
		panic("capacity must be > 0")
	}
	return &CircularBuffer[T]{
		buf:       make([]T, capacity),
		head:      0,
		tail:      0,
		size:      0,
		capacity:  capacity,
		overwrite: overwrite,
	}
}

// Capacity returns buffer capacity.
func (r *CircularBuffer[T]) Capacity() int { return r.capacity }

// Len returns current number of elements.
func (r *CircularBuffer[T]) Len() int { return r.size }

// IsEmpty reports whether buffer is empty.
func (r *CircularBuffer[T]) IsEmpty() bool { return r.size == 0 }

// IsFull reports whether buffer is full.
func (r *CircularBuffer[T]) IsFull() bool { return r.size == r.capacity }

// Enqueue tries to add an element.
// If buffer is full and overwrite==false returns false.
// If buffer is full and overwrite==true it overwrites the oldest element.
func (r *CircularBuffer[T]) Enqueue(item T) bool {
	if r.size == r.capacity {
		if !r.overwrite {
			return false
		}
		// Overwrite oldest: write at tail, advance both head and tail (size stays capacity)
		r.buf[r.tail] = item
		r.tail = (r.tail + 1) % r.capacity
		r.head = r.tail // oldest element moved to new head (equivalently head++)
		return true
	}
	// Not full
	r.buf[r.tail] = item
	r.tail = (r.tail + 1) % r.capacity
	r.size++
	return true
}

// Add forces add with overwrite behavior (convenience).
// If buffer not full — behaves like Enqueue.
// If full — overwrites oldest.
func (r *CircularBuffer[T]) Add(item T) {
	// If not full, just enqueue (ignore error)
	_ = r.Enqueue(item)
}

// Dequeue removes and returns the oldest element.
// If empty returns (zero, false).
func (r *CircularBuffer[T]) Dequeue() (T, bool) {
	var zero T
	if r.size == 0 {
		return zero, false
	}
	val := r.buf[r.head]
	// Optional: zero out slot to help GC for reference types:
	var empty T
	r.buf[r.head] = empty

	r.head = (r.head + 1) % r.capacity
	r.size--
	return val, true
}

// Peek returns the oldest element without removing it.
// If empty returns (zero, ErrEmpty).
func (r *CircularBuffer[T]) Peek() (T, bool) {
	var zero T
	if r.size == 0 {
		return zero, false
	}
	return r.buf[r.head], true
}

func (r *CircularBuffer[T]) PopFirstN(n int) {
	if n <= 0 || r.size == 0 {
		return
	}
	if n >= r.size {
		// Удалено всё — просто сброс буфера
		r.head = 0
		r.tail = 0
		r.size = 0
		return
	}

	r.head = (r.head + n) % r.capacity
	r.size -= n
}

func (r *CircularBuffer[T]) PopLastN(n int) {
	if n <= 0 || r.size == 0 {
		return
	}
	if n >= r.size {
		r.head = 0
		r.tail = 0
		r.size = 0
		return
	}

	r.tail = (r.tail - n + r.capacity) % r.capacity
	r.size -= n
}

// Clear removes all elements.
func (r *CircularBuffer[T]) Clear() {
	if r.size == 0 {
		return
	}
	// zero underlying elements to help GC
	var zero T
	for i := 0; i < r.size; i++ {
		idx := (r.head + i) % r.capacity
		r.buf[idx] = zero
	}
	r.head = 0
	r.tail = 0
	r.size = 0
}

func (r *CircularBuffer[T]) Get(idx int) (T, bool) {
	if idx < 0 || idx >= r.size {
		panic(fmt.Sprintf("index out of range [%d..%d]: %d", 0, r.size, idx))
	}
	var zero T
	if r.size == 0 {
		return zero, false
	}
	val := r.buf[r.head+idx%r.capacity]
	return val, true
}

func (r *CircularBuffer[T]) All() iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		for i := 0; i < r.size; i++ {
			idx := (r.head + i) % r.capacity
			if !yield(i, r.buf[idx]) {
				return
			}
		}
	}
}
