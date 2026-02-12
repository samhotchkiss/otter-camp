package mcp

import "sync"

type resourceSubscriptions struct {
	mu      sync.Mutex
	subs    map[string]map[string]struct{}
	pending map[string]int
}

func newResourceSubscriptions() *resourceSubscriptions {
	return &resourceSubscriptions{
		subs:    make(map[string]map[string]struct{}),
		pending: make(map[string]int),
	}
}

func (r *resourceSubscriptions) subscribe(subscriber, uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[subscriber]; !ok {
		r.subs[subscriber] = make(map[string]struct{})
	}
	r.subs[subscriber][uri] = struct{}{}
}

func (r *resourceSubscriptions) unsubscribe(subscriber, uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subs[subscriber]; !ok {
		return
	}
	delete(r.subs[subscriber], uri)
	if len(r.subs[subscriber]) == 0 {
		delete(r.subs, subscriber)
		delete(r.pending, subscriber)
	}
}

func (r *resourceSubscriptions) notify() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for subscriber := range r.subs {
		r.pending[subscriber]++
	}
}

func (r *resourceSubscriptions) pendingCount(subscriber string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending[subscriber]
}
