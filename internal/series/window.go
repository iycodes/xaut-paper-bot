package series

import "math"

type Window struct {
	values   []float64
	capacity int
	next     int
	full     bool
}

func New(capacity int) *Window {
	if capacity < 2 {
		capacity = 2
	}
	return &Window{values: make([]float64, capacity), capacity: capacity}
}

func (w *Window) Add(v float64) {
	w.values[w.next] = v
	w.next++
	if w.next >= w.capacity {
		w.next = 0
		w.full = true
	}
}

func (w *Window) Len() int {
	if w.full {
		return w.capacity
	}
	return w.next
}

func (w *Window) Values() []float64 {
	n := w.Len()
	out := make([]float64, n)
	if !w.full {
		copy(out, w.values[:n])
		return out
	}
	copy(out, w.values[w.next:])
	copy(out[w.capacity-w.next:], w.values[:w.next])
	return out
}

func (w *Window) Last() (float64, bool) {
	if w.Len() == 0 {
		return 0, false
	}
	i := w.next - 1
	if i < 0 {
		i = w.capacity - 1
	}
	return w.values[i], true
}

func (w *Window) Mean() float64 {
	vals := w.Values()
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func (w *Window) StdDev() float64 {
	vals := w.Values()
	if len(vals) < 2 {
		return 0
	}
	mean := w.Mean()
	var ss float64
	for _, v := range vals {
		d := v - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(vals)-1))
}
