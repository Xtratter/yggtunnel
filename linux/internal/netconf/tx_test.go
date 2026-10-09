package netconf

import (
	"errors"
	"testing"

	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

func noRec(store.PrevState) error { return nil }

func TestTxRollbackOrder(t *testing.T) {
	var order []int
	tx := NewTx(noRec)
	for i := 1; i <= 3; i++ {
		i := i
		if err := tx.Do(store.Step{Kind: "s"}, func() error { return nil }, func() error { order = append(order, i); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Fatalf("order %v", order)
	}
}

func TestTxApplyErrorNotPushed(t *testing.T) {
	var last store.PrevState
	undone := false
	tx := NewTx(func(p store.PrevState) error { last = p; return nil })
	err := tx.Do(store.Step{Kind: "x"}, func() error { return errors.New("no") }, func() error { undone = true; return nil })
	if err == nil {
		t.Fatal("expected error")
	}
	if len(last.Steps) != 0 {
		t.Fatalf("failed step stayed recorded: %+v", last)
	}
	if err := tx.Rollback(); err != nil || undone {
		t.Fatalf("undo of an unapplied step ran: err=%v undone=%v", err, undone)
	}
}

func TestTxRollbackContinuesAfterUndoError(t *testing.T) {
	var ran []string
	tx := NewTx(noRec)
	mk := func(name string, fail bool) {
		tx.Do(store.Step{Kind: name}, func() error { return nil }, func() error {
			ran = append(ran, name)
			if fail {
				return errors.New(name + " failed")
			}
			return nil
		})
	}
	mk("a", false)
	mk("b", true)
	mk("c", false)
	err := tx.Rollback()
	if err == nil || len(ran) != 3 {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

func TestTxPersistsAfterEachStep(t *testing.T) {
	var sizes []int
	tx := NewTx(func(p store.PrevState) error { sizes = append(sizes, len(p.Steps)); return nil })
	for i := 0; i < 3; i++ {
		tx.Do(store.Step{Kind: "s"}, func() error { return nil }, func() error { return nil })
	}
	if len(sizes) != 3 || sizes[0] != 1 || sizes[1] != 2 || sizes[2] != 3 {
		t.Fatalf("sizes %v", sizes)
	}
	tx.Rollback()
	if sizes[len(sizes)-1] != 0 {
		t.Fatalf("rollback did not clear the record: %v", sizes)
	}
}

func TestTxRecorderFailureAbortsStep(t *testing.T) {
	applied := false
	tx := NewTx(func(store.PrevState) error { return errors.New("disk full") })
	if err := tx.Do(store.Step{Kind: "s"}, func() error { applied = true; return nil }, func() error { return nil }); err == nil {
		t.Fatal("expected error")
	}
	if applied {
		t.Fatal("step applied although it could not be recorded")
	}
}
