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

// Undo undoes the last step of that kind now, forgets it and re-records. It does nothing (and returns
// nil) when there is no such step, so it can be called twice.
func (t *Tx) Undo(kind string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := len(t.steps) - 1; i >= 0; i-- {
		if t.steps[i].Kind != kind || i >= len(t.undos) {
			continue
		}
		err := t.undos[i]()
		t.steps = append(t.steps[:i], t.steps[i+1:]...)
		t.undos = append(t.undos[:i], t.undos[i+1:]...)
		if rerr := t.rec(store.PrevState{Steps: t.steps}); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return err
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
