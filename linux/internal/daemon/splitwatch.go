package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	splitInterval      = 60 * time.Second
	splitLookupTimeout = 5 * time.Second
	splitMaxLookups    = 8
)

// splitWatcher resolves the listed domain names and hands their addresses to `apply` (which puts
// them into the firewall sets): at start, every `interval`, and at once when the list changes. A
// name that fails to resolve keeps its last good addresses; the failure is only reported.
type splitWatcher struct {
	lookup        func(ctx context.Context, host string) ([]netip.Addr, error)
	apply         func([]netip.Addr) error
	interval      time.Duration
	lookupTimeout time.Duration
	newTicker     func(time.Duration) (<-chan time.Time, func())

	mu       sync.Mutex
	domains  []string
	last     map[string][]netip.Addr
	resolved int
	at       time.Time
	err      error

	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	started bool
	stopped bool
}

func newSplitWatcher(lookup func(context.Context, string) ([]netip.Addr, error), apply func([]netip.Addr) error) *splitWatcher {
	return &splitWatcher{
		lookup: lookup, apply: apply,
		interval: splitInterval, lookupTimeout: splitLookupTimeout,
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
		last: map[string][]netip.Addr{},
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
	}
}

// Start begins resolving; the first round runs at once.
func (w *splitWatcher) Start(domains []string) {
	w.mu.Lock()
	if w.started || w.stopped {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.domains = append([]string(nil), domains...)
	w.mu.Unlock()
	go w.loop()
}

// SetDomains replaces the list and resolves at once. Safe to call after Stop (it does nothing then).
func (w *splitWatcher) SetDomains(domains []string) {
	w.mu.Lock()
	w.domains = append([]string(nil), domains...)
	stopped := w.stopped
	w.mu.Unlock()
	if stopped {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default: // a round is already pending
	}
}

// Stop ends the loop and waits for it. Calling it twice is fine.
func (w *splitWatcher) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	started := w.started
	close(w.stop)
	w.mu.Unlock()
	if started {
		<-w.done
	}
}

// Status is how many addresses are in the sets now, when they were set, and the last problem.
func (w *splitWatcher) Status() (resolved int, at time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resolved, w.at, w.err
}

func (w *splitWatcher) loop() {
	defer close(w.done)
	ticks, stopTicker := w.newTicker(w.interval)
	defer stopTicker()
	w.round()
	for {
		select {
		case <-w.stop:
			return
		case <-ticks:
			w.round()
		case <-w.wake:
			w.round()
		}
	}
}

type lookupResult struct {
	host  string
	addrs []netip.Addr
	err   error
}

// round resolves every listed name (at most splitMaxLookups at a time, each abandoned after the
// timeout) and applies the union of the answers.
func (w *splitWatcher) round() {
	w.mu.Lock()
	domains := append([]string(nil), w.domains...)
	w.mu.Unlock()

	results := make([]lookupResult, len(domains))
	sem := make(chan struct{}, splitMaxLookups)
	var wg sync.WaitGroup
	for i, host := range domains {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, host string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), w.lookupTimeout)
			defer cancel()
			ch := make(chan lookupResult, 1) // buffered: an abandoned lookup can still finish and be dropped
			go func() {
				a, err := w.lookup(ctx, host)
				ch <- lookupResult{host, a, err}
			}()
			select {
			case r := <-ch:
				results[i] = r
			case <-ctx.Done():
				results[i] = lookupResult{host: host, err: errors.New("lookup timed out")}
			}
		}(i, host)
	}
	wg.Wait()

	w.mu.Lock()
	keep := map[string][]netip.Addr{}
	var problems []string
	seen := map[netip.Addr]bool{}
	var union []netip.Addr
	add := func(addrs []netip.Addr) {
		for _, a := range addrs {
			a = a.Unmap()
			if a.IsValid() && !seen[a] {
				seen[a] = true
				union = append(union, a)
			}
		}
	}
	for _, r := range results {
		addrs := r.addrs
		if r.err != nil || len(addrs) == 0 {
			if r.err == nil {
				r.err = errors.New("no addresses")
			}
			problems = append(problems, fmt.Sprintf("%s: %v", r.host, r.err))
			addrs = w.last[r.host] // keep what worked before
		}
		keep[r.host] = addrs
		add(addrs)
	}
	w.last = keep
	w.mu.Unlock()

	applyErr := w.apply(union)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.resolved, w.at = len(union), time.Now()
	switch {
	case applyErr != nil:
		w.err = fmt.Errorf("applying addresses: %w", applyErr)
	case len(problems) > 0:
		w.err = errors.New(strings.Join(problems, "; "))
	default:
		w.err = nil
	}
}
