package di

// Branches only an unexported call reaches; edges_test.go has the rest.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestWantKindString(t *testing.T) {
	for k, want := range map[wantKind]string{wantValue: "a dependency", wantOptional: "Optional", wantGroup: "AllOf"} {
		if got := k.String(); got != want {
			t.Errorf("%d: got %q, want %q", k, got, want)
		}
	}
}

func TestModuleNameOfNil(t *testing.T) {
	if got := moduleName(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

// emptyJoin unwraps to no errors at all, which no stdlib error does.
type emptyJoin struct{}

func (emptyJoin) Error() string   { return "empty" }
func (emptyJoin) Unwrap() []error { return nil }

func TestOnlyCancellation(t *testing.T) {
	failure := errors.New("failure")
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"canceled", context.Canceled, true},
		{"wrapped", fmt.Errorf("worker: %w", context.Canceled), true},
		{"joined cancellations", errors.Join(context.Canceled, context.Canceled), true},
		{"deadline", context.DeadlineExceeded, false},
		{"failure", failure, false},
		{"joined with a failure", errors.Join(context.Canceled, failure), false},
		{"wrapped failure", fmt.Errorf("worker: %w", failure), false},
		{"empty join", emptyJoin{}, false},
	} {
		if got := onlyCancellation(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
