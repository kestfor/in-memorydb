package structs

import (
	"errors"
	"fmt"
)

var ErrCannotMerge = errors.New("cannot merge ranges")

// Range представляет диапазон sequence numbers [Start, End] включительно
type Range struct {
	Start uint64 `json:"s"`
	End   uint64 `json:"e"`
}

func (r Range) Len() uint64 {
	return r.End - r.Start + 1
}

func (r Range) ContainsValue(v uint64) bool {
	return v >= r.Start && v <= r.End
}

func (r Range) ContainsOther(o Range) bool {
	return r.ContainsValue(o.Start) && r.ContainsValue(o.End)
}

func (r Range) Equals(o Range) bool {
	return r.Start == o.Start && r.End == o.End
}

func (r Range) Merge(other Range) (Range, error) {
	if other.Start > r.End+1 || r.Start > other.End+1 {
		return Range{}, fmt.Errorf("ranges are not mergeable (%s, %s): %w", r, other, ErrCannotMerge)
	}
	r.Start = min(other.Start, r.Start)
	r.End = max(other.End, r.End)
	return r, nil
}

func (r Range) String() string {
	return fmt.Sprintf("[%d-%d]", r.Start, r.End)
}

func (r Range) Split(pivot uint64) (left, right Range) {
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
