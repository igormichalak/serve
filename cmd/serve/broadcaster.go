package main

import "sync"

type broadcaster struct {
	channels map[chan struct{}]struct{}
	mu       sync.RWMutex
}

func newBroadcaster() *broadcaster {
	return &broadcaster{
		channels: make(map[chan struct{}]struct{}),
	}
}

func (b *broadcaster) notify() {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.channels {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *broadcaster) subscribe() chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan struct{})
	b.channels[ch] = struct{}{}
	return ch
}

func (b *broadcaster) unsubscribe(ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.channels, ch)
	close(ch)
}
