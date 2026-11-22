package main

import (
	"net/http"
	stdsync "sync"
	"time"
)

// Rate limits apply across pages AND books, including reconciliation requests.
// Only the production client is paced; injected test clients remain deterministic.
type pacedTransport struct {
	base  http.RoundTripper
	mu    stdsync.Mutex
	last  time.Time
	lists map[string]time.Time
}

func (p *pacedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	wait := time.Until(p.last.Add(260 * time.Millisecond))
	key := ""
	if r.Method == http.MethodGet && (r.URL.Path == "/api/v2/books/" || r.URL.Path == "/api/v2/highlights/") {
		key = r.URL.Path
		if d := time.Until(p.lists[key].Add(3100 * time.Millisecond)); d > wait {
			wait = d
		}
	}
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			p.mu.Unlock()
			return nil, r.Context().Err()
		}
	}
	p.last = time.Now()
	if key != "" {
		p.lists[key] = p.last
	}
	p.mu.Unlock()
	return p.base.RoundTrip(r)
}
