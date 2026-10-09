package daemon

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeResolve struct {
	mu                    sync.Mutex
	answers               map[string][]netip.Addr
	errs                  map[string]error
	calls                 map[string]int
	block                 chan struct{} // when set, lookups of "slow.example.com" block on it, ignoring ctx
	inFlight, maxInFlight atomic.Int32
	delay                 time.Duration
}

func (f *fakeResolve) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	n := f.inFlight.Add(1)
	for {
		m := f.maxInFlight.Load()
		if n <= m || f.maxInFlight.CompareAndSwap(m, n) {
			break
		}
	}
	defer f.inFlight.Add(-1)
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[host]++
	a, e, b := f.answers[host], f.errs[host], f.block
	f.mu.Unlock()
	if host == "slow.example.com" && b != nil {
		<-b
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return a, e
}

func (f *fakeResolve) set(host string, addrs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answers == nil {
		f.answers = map[string][]netip.Addr{}
	}
	var out []netip.Addr
	for _, s := range addrs {
		out = append(out, netip.MustParseAddr(s))
	}
	f.answers[host] = out
}

func (f *fakeResolve) fail(host string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[host] = err
}

type applied struct {
	mu   sync.Mutex
	last []netip.Addr
	n    int
}

func (a *applied) apply(addrs []netip.Addr) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = append([]netip.Addr(nil), addrs...)
	a.n++
	return nil
}

func (a *applied) get() ([]string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var s []string
	for _, x := range a.last {
		s = append(s, x.String())
	}
	sort.Strings(s)
	return s, a.n
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached in 2 s")
}

// newTestWatcher returns a watcher whose ticks are driven by the test.
func newTestWatcher(f *fakeResolve, ap *applied) (*splitWatcher, chan time.Time) {
	tick := make(chan time.Time)
	w := newSplitWatcher(f.lookup, ap.apply)
	w.newTicker = func(time.Duration) (<-chan time.Time, func()) { return tick, func() {} }
	return w, tick
}

func TestWatcherResolvesImmediatelyAndPeriodically(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	f.set("a.example.com", "192.0.2.1", "2001:db8::1")
	f.set("b.example.com", "192.0.2.2")
	w, tick := newTestWatcher(f, ap)
	w.Start([]string{"a.example.com", "b.example.com"})
	defer w.Stop()
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 3 })
	f.set("b.example.com", "192.0.2.22") // the name moved
	tick <- time.Now()
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 3 && s[2] == "2001:db8::1" && s[1] == "192.0.2.22" })
	n, _, err := w.Status()
	if n != 3 || err != nil {
		t.Fatalf("status n=%d err=%v", n, err)
	}
}

func TestWatcherLookupFailureKeepsOldAddressesAndReportsError(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	f.set("a.example.com", "192.0.2.1")
	f.set("b.example.com", "192.0.2.2")
	w, tick := newTestWatcher(f, ap)
	w.Start([]string{"a.example.com", "b.example.com"})
	defer w.Stop()
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 2 })
	f.fail("b.example.com", errors.New("servfail"))
	tick <- time.Now()
	waitFor(t, func() bool { _, _, err := w.Status(); return err != nil })
	if s, _ := ap.get(); len(s) != 2 {
		t.Fatalf("the failed name lost its addresses: %v", s)
	}
	_, _, err := w.Status()
	if err == nil || !contains(err.Error(), "b.example.com") {
		t.Fatalf("error %v should name the failing domain", err)
	}
	f.fail("b.example.com", nil)
	tick <- time.Now()
	waitFor(t, func() bool { _, _, err := w.Status(); return err == nil })
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestWatcherSetDomainsResolvesAtOnce(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	f.set("a.example.com", "192.0.2.1")
	f.set("c.example.com", "192.0.2.3")
	w, _ := newTestWatcher(f, ap)
	w.Start([]string{"a.example.com"})
	defer w.Stop()
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 1 })
	w.SetDomains([]string{"a.example.com", "c.example.com"}) // no tick: the change itself triggers a lookup
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 2 })
	w.SetDomains(nil)
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 0 })
}

func TestWatcherWithNoDomainsAppliesNothingAndKeepsRunning(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	w, _ := newTestWatcher(f, ap)
	w.Start(nil)
	defer w.Stop()
	waitFor(t, func() bool { _, n := ap.get(); return n >= 1 })
	if len(f.calls) != 0 {
		t.Fatalf("lookups for an empty list: %v", f.calls)
	}
}

func TestWatcherStopStopsGoroutines(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	f.set("a.example.com", "192.0.2.1")
	w, tick := newTestWatcher(f, ap)
	w.Start([]string{"a.example.com"})
	waitFor(t, func() bool { _, n := ap.get(); return n >= 1 })
	w.Stop()
	_, before := ap.get()
	select {
	case tick <- time.Now():
		t.Fatal("the loop still reads ticks after Stop")
	case <-time.After(50 * time.Millisecond):
	}
	w.SetDomains([]string{"x.example.com"}) // must not block or panic after Stop
	time.Sleep(30 * time.Millisecond)
	if _, after := ap.get(); after != before {
		t.Fatal("applied after Stop")
	}
	w.Stop() // twice is fine
}

func TestWatcherLimitsConcurrentLookups(t *testing.T) {
	f, ap := &fakeResolve{delay: 10 * time.Millisecond}, &applied{}
	var names []string
	for i := 0; i < 40; i++ {
		h := "h" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".example.com"
		names = append(names, h)
		f.set(h, "192.0.2.1")
	}
	w, _ := newTestWatcher(f, ap)
	w.Start(names)
	defer w.Stop()
	waitFor(t, func() bool { _, n := ap.get(); return n >= 1 })
	if m := f.maxInFlight.Load(); m > 8 || m < 2 {
		t.Fatalf("max concurrent lookups %d, want between 2 and 8", m)
	}
}

func TestWatcherAbandonsSlowLookup(t *testing.T) {
	f, ap := &fakeResolve{block: make(chan struct{})}, &applied{}
	defer close(f.block)
	f.set("a.example.com", "192.0.2.1")
	f.set("slow.example.com", "192.0.2.9")
	w, _ := newTestWatcher(f, ap)
	w.lookupTimeout = 60 * time.Millisecond
	w.Start([]string{"a.example.com", "slow.example.com"})
	defer w.Stop()
	waitFor(t, func() bool { s, _ := ap.get(); return len(s) == 1 })
	_, _, err := w.Status()
	if err == nil || !contains(err.Error(), "slow.example.com") {
		t.Fatalf("error %v should name the slow domain", err)
	}
}

func TestWatcherDeduplicatesAddresses(t *testing.T) {
	f, ap := &fakeResolve{}, &applied{}
	f.set("a.example.com", "192.0.2.1", "192.0.2.1")
	f.set("b.example.com", "192.0.2.1", "::ffff:192.0.2.1")
	w, _ := newTestWatcher(f, ap)
	w.Start([]string{"a.example.com", "b.example.com"})
	defer w.Stop()
	waitFor(t, func() bool { _, n := ap.get(); return n >= 1 })
	if s, _ := ap.get(); len(s) != 1 || s[0] != "192.0.2.1" {
		t.Fatalf("%v", s)
	}
}
