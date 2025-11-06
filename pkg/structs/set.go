package structs

import "iter"

type empty = struct{}

// Set — простое множество для значений типа T
type Set[T comparable] map[T]empty

// NewSet создаёт пустое множество
func NewSet[T comparable](values ...T) Set[T] {
	res := make(Set[T])
	for _, v := range values {
		res[v] = empty{}
	}
	return res
}

// Add добавляет элемент в множество
func (s Set[T]) Add(value T) {
	s[value] = empty{}
}

// Remove удаляет элемент из множества
func (s Set[T]) Remove(value T) {
	delete(s, value)
}

// Has проверяет наличие элемента
func (s Set[T]) Contains(value T) bool {
	_, exists := s[value]
	return exists
}

// Size возвращает количество элементов
func (s Set[T]) Size() int {
	return len(s)
}

// Values возвращает все элементы множества
func (s Set[T]) Slice() []T {
	values := make([]T, 0, len(s))
	for v := range s {
		values = append(values, v)
	}
	return values
}

func (s Set[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		for v := range s {
			if !yield(v) {
				return
			}
		}
	}
}

// Clear очищает множество
func (s Set[T]) Clear() {
	for k := range s {
		delete(s, k)
	}
}

// Clone создаёт копию множества
func (s Set[T]) Clone() Set[T] {
	clone := NewSet[T]()
	for v := range s {
		clone[v] = struct{}{}
	}
	return clone
}

// Union объединяет текущее множество с другим
func (s Set[T]) Union(other Set[T]) Set[T] {
	result := s.Clone()
	for v := range other {
		result[v] = struct{}{}
	}
	return result
}

// Intersect возвращает пересечение двух множеств
func (s Set[T]) Intersect(other Set[T]) Set[T] {
	result := NewSet[T]()
	for v := range s {
		if _, ok := other[v]; ok {
			result[v] = struct{}{}
		}
	}
	return result
}

// Difference возвращает разность множеств (элементы, которые есть в s, но нет в other)
func (s Set[T]) Difference(other Set[T]) Set[T] {
	result := NewSet[T]()
	for v := range s {
		if _, ok := other[v]; !ok {
			result[v] = struct{}{}
		}
	}
	return result
}

// IsSubsetOf проверяет, является ли s подмножеством other
func (s Set[T]) IsSubsetOf(other Set[T]) bool {
	for v := range s {
		if _, ok := other[v]; !ok {
			return false
		}
	}
	return true
}
