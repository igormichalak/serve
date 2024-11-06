package main

import (
	"sync"
	"time"
)

type debouncer struct {
	timers map[string]*time.Timer
	after  time.Duration
	mu     sync.Mutex
}

func newDebouncer(after time.Duration) *debouncer {
	return &debouncer{
		timers: make(map[string]*time.Timer),
		after:  after,
	}
}

func (d *debouncer) Call(key string, f func()) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if timer, ok := d.timers[key]; ok {
		timer.Stop()
	}
	d.timers[key] = time.AfterFunc(d.after, f)
}
