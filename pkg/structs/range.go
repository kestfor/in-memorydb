package structs

import (
	"errors"
	"fmt"
)

var ErrCannotMerge = errors.New("cannot merge ranges")

// Range представляет диапазон sequence numbers [Start, End] включительно
type Range struct {
	Start int64 `json:"s"`
	End   int64 `json:"e"`
}

func (r Range) Len() int64 {
	return r.End - r.Start + 1
}

func (r Range) ContainsValue(v int64) bool {
	return v >= r.Start && v <= r.End
}

func (r Range) ContainsOther(o Range) bool {
	return r.ContainsValue(o.Start) && r.ContainsValue(o.End)
}

func (r Range) Equals(o Range) bool {
	return r.Start == o.Start && r.End == o.End
}

func (r Range) Merge(other Range) (Range, error) {
	if !(r.Start <= other.Start && r.End <= other.End) && !(other.Start <= r.Start && other.End <= r.End) {
		return Range{}, fmt.Errorf("ranges are not mergeable: %w", ErrCannotMerge)
	}
	r.Start = min(other.Start, r.Start)
	r.End = max(other.End, r.End)
	return r, nil
}

func (r Range) String() string {
	return fmt.Sprintf("[%d-%d]", r.Start, r.End)
}

func (r Range) Split(pivot int64) (left, right Range) {
	if pivot < r.Start || pivot > r.End {
		left = r
		right = r
		return left, right
	}
	left.Start = r.Start
	left.End = pivot

	right.Start = pivot
	right.End = r.End
	return left, right
}
