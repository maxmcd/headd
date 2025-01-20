package synctyped

import "sync"

type Map[T any] struct {
	sync.Map
}

func (m *Map[T]) Store(k string, t T) {
	m.Map.Store(k, t)
}

func (m *Map[T]) Load(k string) (t T, ok bool) {
	v, ok := m.Map.Load(k)
	if !ok {
		return t, ok
	}

	return v.(T), true
}

func (m *Map[T]) LoadOrStore(k string, v T) (actual T, loaded bool) {
	a, loaded := m.Map.LoadOrStore(k, v)
	return a.(T), loaded
}

func (m *Map[T]) Range(f func(k string, v T) bool) {
	m.Map.Range(func(key, value any) bool {
		return f(key.(string), value.(T))
	})
}
