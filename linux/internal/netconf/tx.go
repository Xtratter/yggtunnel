// Package netconf applies the system changes of a tunnel (interface, addresses, routes, rules,
// traffic mark) as a transaction: every step is recorded before it runs and can be undone,
// even by a later process that only has the record.
package netconf

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

// Tx is a stack of applied steps.
type Tx struct {
	mu    sync.Mutex
	rec   func(store.PrevState) error
	steps []store.Step
	undos []func() error
}

// NewTx creates an empty transaction; rec persists the recorded steps after every change.
func NewTx(rec func(store.PrevState) error) *Tx { return &Tx{rec: rec} }

// Do records the step, applies it and keeps undo for Rollback. The record is written first, so a
// crash between recording and applying leaves a step whose undo is harmless (undos ignore
// "not found"). If recording fails the step is not applied; if applying fails the record is
// withdrawn and nothing is pushed.
func (t *Tx) Do(step store.Step, apply, undo func() error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.steps = append(t.steps, step)
	if err := t.rec(store.PrevState{Steps: t.steps}); err != nil {
		t.steps = t.steps[:len(t.steps)-1]
		return fmt.Errorf("record %s: %w", step.Kind, err)
	}
	if err := apply(); err != nil {
		t.steps = t.steps[:len(t.steps)-1]
		_ = t.rec(store.PrevState{Steps: t.steps})
		return fmt.Errorf("%s: %w", step.Kind, err)
	}
	t.undos = append(t.undos, undo)
	return nil
}

// Rollback undoes the steps in reverse order, continuing past failures, and clears the record.
func (t *Tx) Rollback() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var errs []error
	for i := len(t.undos) - 1; i >= 0; i-- {
		if err := t.undos[i](); err != nil {
			errs = append(errs, err)
		}
	}
	t.undos, t.steps = nil, nil
	if err := t.rec(store.PrevState{}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Has reports whether a step of that kind is recorded.
func (t *Tx) Has(kind string) bool { return t.Count(kind) > 0 }

// Count is the number of recorded steps of that kind.
func (t *Tx) Count(kind string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, s := range t.steps {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// Undo undoes the last step of that kind now and forgets it. If the undo fails the step is KEPT
// (and recorded), so that Rollback, panic or crash recovery can try again: forgetting a step whose
// undo failed could leave its changes behind with no record of them. It does nothing (and returns
// nil) when there is no such step, so it can be called twice.
func (t *Tx) Undo(kind string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.steps) - 1; i >= 0; i-- {
		if t.steps[i].Kind != kind || i >= len(t.undos) {
			continue
		}
		if err := t.undos[i](); err != nil {
			return err
		}
		t.steps = append(t.steps[:i], t.steps[i+1:]...)
		t.undos = append(t.undos[:i], t.undos[i+1:]...)
		return t.rec(store.PrevState{Steps: t.steps})
	}
	return nil
}

// RecoverFrom undoes steps recorded by a process that died, using only the record.
func RecoverFrom(prev store.PrevState) error {
	var errs []error
	for i := len(prev.Steps) - 1; i >= 0; i-- {
		if err := undoStep(prev.Steps[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func undoStep(s store.Step) error {
	switch s.Kind {
	case "link":
		return delLink(s.Args["name"])
	case "addr":
		return delAddr(s.Args)
	case "route":
		return delRoute(s.Args)
	case "rule":
		return delRule(s.Args)
	case "nft":
		return delMarkTable(s.Args["table"])
	case "killswitch":
		return delMarkTable(s.Args["table"])
	case "dry", "dry-killswitch": // recorded by `--dry-run`; nothing was changed
		return nil
	}
	return fmt.Errorf("unknown step kind %q", s.Kind)
}
