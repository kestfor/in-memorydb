package structs_test

import (
	"in-memorydb/pkg/structs"
	"slices"
	"testing"
)

func TestCircularBufferBasic(t *testing.T) {
	r := structs.NewCircularBuffer[int](3, false)

	ok := r.Enqueue(1)
	if !ok {
		t.Fatalf("failed enqueue")
	}
	_ = r.Enqueue(2)
	_ = r.Enqueue(3)

	if r.Len() != 3 {
		t.Fatalf("expected len=3 got %d", r.Len())
	}
	if !r.IsFull() {
		t.Fatalf("expected full buffer")
	}

	// no overwrite: must fail
	if r.Enqueue(4) {
		t.Fatalf("expected enqueue fail due to full buffer")
	}

	v, ok := r.Dequeue()
	if !ok || v != 1 {
		t.Fatalf("expected 1 got %v", v)
	}
	v, ok = r.Dequeue()
	if !ok || v != 2 {
		t.Fatalf("expected 2 got %v", v)
	}
	v, ok = r.Dequeue()
	if !ok || v != 3 {
		t.Fatalf("expected 3 got %v", v)
	}

	_, ok = r.Dequeue()
	if ok {
		t.Fatalf("expected empty buffer")
	}
}

func TestCircularBufferOverwrite(t *testing.T) {
	r := structs.NewCircularBuffer[int](3, true)

	r.Enqueue(1)
	r.Enqueue(2)
	r.Enqueue(3)

	// buffer full, overwrite oldest (1) with 4
	r.Enqueue(4)

	got := slices.Collect(r.All())
	want := []int{2, 3, 4}

	if !slices.Equal(got, want) {
		t.Fatalf("overwrite mismatch: got %v want %v", got, want)
	}

	// again overwrite (2) with 5
	r.Enqueue(5)

	got = slices.Collect(r.All())
	want = []int{3, 4, 5}

	if !slices.Equal(got, want) {
		t.Fatalf("overwrite mismatch: got %v want %v", got, want)
	}
}

func TestCircularBufferWrapAround(t *testing.T) {
	r := structs.NewCircularBuffer[int](3, false)

	r.Enqueue(1)
	r.Enqueue(2)
	r.Enqueue(3)

	// dequeue two to force head shift
	r.Dequeue() // 1
	r.Dequeue() // 2

	// tail should wrap when adding
	r.Enqueue(4)
	r.Enqueue(5) // tail wraps

	got := slices.Collect(r.All())
	want := []int{3, 4, 5}

	if !slices.Equal(got, want) {
		t.Fatalf("wrap mismatch: got %v want %v", got, want)
	}
}

func TestCircularBufferGet(t *testing.T) {
	r := structs.NewCircularBuffer[int](5, false)

	for i := 1; i <= 5; i++ {
		r.Enqueue(i)
	}

	for i := 0; i < 5; i++ {
		v, ok := r.Get(i)
		if !ok {
			t.Fatalf("Get failed at i=%d", i)
		}
		if v != i+1 {
			t.Fatalf("Get(%d)=%d, want %d", i, v, i+1)
		}
	}

	// Out of range panic
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on out-of-range index")
		}
	}()
	r.Get(6)
}

func TestCircularBufferIterator(t *testing.T) {
	r := structs.NewCircularBuffer[string](4, false)
	r.Enqueue("a")
	r.Enqueue("b")
	r.Enqueue("c")

	collected := slices.Collect(r.All())
	want := []string{"a", "b", "c"}

	if !slices.Equal(collected, want) {
		t.Fatalf("All mismatch: got %v want %v", collected, want)
	}

	// Break early
	count := 0
	for v := range r.All() {
		_ = v
		count++
		if count == 1 {
			break
		}
	}
	if count != 1 {
		t.Fatalf("iterator early stop failed")
	}
}

func TestCircularBufferClear(t *testing.T) {
	r := structs.NewCircularBuffer[int](3, false)
	r.Enqueue(10)
	r.Enqueue(20)
	r.Enqueue(30)

	r.Clear()

	if !r.IsEmpty() {
		t.Fatalf("expected empty after Clear")
	}
	if r.Len() != 0 {
		t.Fatalf("expected len=0 got %d", r.Len())
	}

	r.Enqueue(1)
	r.Enqueue(2)

	got := slices.Collect(r.All())
	want := []int{1, 2}

	if !slices.Equal(got, want) {
		t.Fatalf("after clear mismatch: got %v want %v", got, want)
	}
}

func TestPeek(t *testing.T) {
	r := structs.NewCircularBuffer[int](3, false)

	_, ok := r.Peek()
	if ok {
		t.Fatalf("expected empty peek")
	}

	r.Enqueue(10)
	r.Enqueue(20)

	v, ok := r.Peek()
	if !ok || v != 10 {
		t.Fatalf("expected peek=10 got %v", v)
	}

	// ensure Peek does not remove
	v2, ok := r.Dequeue()
	if !ok || v2 != 10 {
		t.Fatalf("peek affected dequeue")
	}
}
