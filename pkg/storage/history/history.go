package history

import (
	. "in-memorydb/pkg/structs"
	"sort"
)

// NodeHistory — хранит всю историю seq-id по ноде.
// Хранение — упорядоченный список непересекающихся интервалов.
type NodeHistory struct {
	Ranges []Range // sorted by Start
}

type History struct {
	nodes map[string]*NodeHistory
}

func NewHistory() *History {
	return &History{
		nodes: make(map[string]*NodeHistory),
	}
}

func (h *History) getOrCreate(node string) *NodeHistory {
	if nh, ok := h.nodes[node]; ok {
		return nh
	}
	nh := &NodeHistory{}
	h.nodes[node] = nh
	return nh
}

func (h *History) Add(node string, seq uint64) {
	h.AddRange(node, Range{Start: seq, End: seq})
}

func (h *History) AddRange(node string, r Range) {
	if r.Start > r.End {
		return
	}
	nh := h.getOrCreate(node)
	insertAndMerge(&nh.Ranges, r)
}

// Вставляет диапазон r в slices ranges[], поддерживая сортировку и слияние
func insertAndMerge(rs *[]Range, r Range) {
	if len(*rs) == 0 {
		*rs = []Range{r}
		return
	}

	// Бинарный поиск позиции вставки (первый >= r.Start)
	n := len(*rs)
	idx := sort.Search(n, func(i int) bool {
		return (*rs)[i].Start >= r.Start
	})

	// Вставляем r на позицию idx
	*rs = append(*rs, Range{})
	copy((*rs)[idx+1:], (*rs)[idx:])
	(*rs)[idx] = r
	n++ // Теперь длина увеличилась

	// Находим левую границу зоны слияния
	startIdx := idx
	for startIdx > 0 && (*rs)[startIdx-1].End+1 >= (*rs)[startIdx].Start {
		startIdx--
	}

	// Находим правую границу зоны слияния
	endIdx := idx
	for endIdx+1 < n && (*rs)[endIdx].End+1 >= (*rs)[endIdx+1].Start {
		endIdx++
	}

	// Если нет зоны слияния (startIdx == endIdx == idx), ничего не делаем
	if startIdx == endIdx {
		return
	}

	// Сливаем все от startIdx до endIdx в один интервал
	merged := (*rs)[startIdx]
	for i := startIdx + 1; i <= endIdx; i++ {
		merged.Start = min(merged.Start, (*rs)[i].Start)
		merged.End = max(merged.End, (*rs)[i].End)
	}

	// Заменяем зону на merged: копируем правую часть назад
	copy((*rs)[startIdx+1:], (*rs)[endIdx+1:])
	(*rs)[startIdx] = merged
	// Укорачиваем слайс
	*rs = (*rs)[:n-(endIdx-startIdx)]
}

func (h *History) Has(node string, seq uint64) bool {
	nh, ok := h.nodes[node]
	if !ok {
		return false
	}
	return hasInRanges(nh.Ranges, seq)
}

// HasRange checks whether a node has a range that fully contains the provided range. Returns true if contained, false otherwise.
func (h *History) HasRange(node string, r Range) bool {
	nh, ok := h.nodes[node]
	if !ok {
		return false
	}
	for _, present := range nh.Ranges {
		if present.ContainsOther(r) {
			return true
		}
	}
	return false
}

func hasInRanges(rs []Range, seq uint64) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].Start > seq })
	if i == 0 {
		return false
	}
	r := rs[i-1]
	return r.Start <= seq && seq <= r.End
}

/*
Diff(node, remoteLast) возвращает список диапазонов,
которые remote не видел.

remoteLast = последняя seq, которую знает remote для этой node.

Важно:
— Для истории с дырками работает корректно.
— Всегда возвращает диапазоны, которые полностью покрывают пропущенные seq.
*/
func (h *History) Diff(node string, remoteLast uint64) []Range {
	nh, ok := h.nodes[node]
	if !ok {
		return nil
	}

	var out []Range

	for _, r := range nh.Ranges {
		if r.End <= remoteLast {
			continue
		}
		start := max(r.Start, remoteLast+1)
		out = append(out, Range{Start: start, End: r.End})
	}

	return out
}

// VectorClockMax returns, for each node, the maximum seq we have (max End of ranges).
// If node absent => omitted from map.
func (h *History) VectorClockMax() map[string]uint64 {
	out := make(map[string]uint64, len(h.nodes))
	for node, nh := range h.nodes {
		if len(nh.Ranges) == 0 {
			continue
		}
		// ranges sorted, so last range has max End
		last := nh.Ranges[len(nh.Ranges)-1]
		out[node] = last.End
	}
	return out
}

// VectorClockContiguous returns, for each node, the largest T such that we have
// all seq <= T. This scans sorted ranges and accumulates contiguous coverage.
// Example: ranges [1..5],[7..10] => contiguous = 5
// If first range doesn't start at 1 (or minimal expected start), contiguous will be 0
// until you have coverage from the base sequence; algorithm assumes seq are positive uint64.
func (h *History) VectorClockContiguous() map[string]uint64 {
	out := make(map[string]uint64, len(h.nodes))
	for node, nh := range h.nodes {
		if len(nh.Ranges) == 0 {
			continue
		}
		contig := uint64(0)
		// iterate ranges in order
		for _, r := range nh.Ranges {
			// if range starts at or before contig+1, we can extend contig
			if r.Start <= contig+1 {
				// extend contig to r.End
				if r.End > contig {
					contig = r.End
				}
				// continue to next range
			} else {
				// gap detected: r.Start > contig+1 -> contiguous prefix stops here
				break
			}
		}
		out[node] = contig
	}
	return out
}

// DiffAll computes, for a remote vector (remote[node] = last seq remote has),
// which ranges local can send to bring remote up-to-date.
// Semantics: If local has ranges [s..e] and remoteLast < e, we return [max(s, remoteLast+1)..e].
// If remote does not contain a node, remoteLast is treated as 0 (or absent).
func (h *History) DiffAll(remote map[string]uint64) map[string][]Range {
	out := make(map[string][]Range)
	// For every node local knows about, compute missing vs remote
	for node, nh := range h.nodes {
		remoteLast := remote[node] // zero if not present
		if len(nh.Ranges) == 0 {
			continue
		}
		// if remoteLast >= local max -> skip
		localMax := nh.Ranges[len(nh.Ranges)-1].End
		if remoteLast >= localMax {
			continue
		}
		// collect needed pieces
		var missing []Range
		for _, r := range nh.Ranges {
			if r.End <= remoteLast {
				continue
			}
			start := r.Start
			if remoteLast+1 > start {
				start = remoteLast + 1
			}
			if start <= r.End {
				missing = append(missing, Range{Start: start, End: r.End})
			}
		}
		if len(missing) > 0 {
			out[node] = missing
		}
	}
	// Optionally: consider nodes present in remote but not local -> nothing to send
	return out
}
