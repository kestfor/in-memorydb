package v1

import "container/heap"

type expiryHeap []markItem

func (h expiryHeap) Len() int {
	return len(h)
}

func (h expiryHeap) Less(i, j int) bool {
	return h[i].expiryAt < h[j].expiryAt
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *expiryHeap) Push(x any) {
	*h = append(*h, x.(markItem))
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func (h *expiryHeap) Peek() (markItem, bool) {
	if len(*h) == 0 {
		return markItem{}, false
	}
	x := (*h)[0]
	return x, true
}

func newExpiryHeap() *expiryHeap {
	h := expiryHeap{}
	heap.Init(&h)
	return &h
}
