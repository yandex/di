package di_test

// Scope.Ready: closed by a successful return of the nearest Start at or
// above the scope, and never by a failed one.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.yandex/di"
)

type readyServer struct{}

type readyLater struct{}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestReadyClosesWhenStartSucceeds(t *testing.T) {
	s := di.New()
	before := s.Child("before")
	early, childEarly := s.Ready(), before.Ready()
	if isClosed(early) || isClosed(childEarly) {
		t.Fatal("Ready closed before Start")
	}
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	after := s.Child("after")
	for name, ch := range map[string]<-chan struct{}{
		"asked before Start": early,
		"asked after Start":  s.Ready(),
		"child asked before": childEarly,
		"child made before":  before.Ready(),
		"child made after":   after.Ready(),
	} {
		if !isClosed(ch) {
			t.Errorf("%s: Ready not closed", name)
		}
	}
	if err := s.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !isClosed(s.Ready()) {
		t.Error("Ready reopened by Stop")
	}
}

// A server that serves only once Ready is closed never serves when a service
// started after it fails, and its worker ends with the rollback.
func TestReadyStaysOpenWhenALaterStartFails(t *testing.T) {
	s := di.New()
	waiting := make(chan struct{})
	var served, returned atomic.Bool
	s.Provide(func(*di.Scope) *readyServer { return &readyServer{} }).
		Eager().
		Go(func(ctx context.Context, _ *readyServer) error {
			defer returned.Store(true)
			close(waiting)
			select {
			case <-s.Ready():
				served.Store(true)
			case <-ctx.Done():
			}
			return nil
		})
	boom := errors.New("boom")
	s.Provide(func(*di.Scope) *readyLater { return &readyLater{} }).
		Eager().
		OnStart(func(context.Context, *readyLater) error {
			<-waiting
			return boom
		})

	if err := s.Start(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want %v", err, boom)
	}
	if served.Load() {
		t.Error("worker served after a failed Start")
	}
	if !returned.Load() {
		t.Error("rollback returned before the worker did")
	}
	if isClosed(s.Ready()) {
		t.Error("Ready closed by a failed Start")
	}
}

// A child stopped before its parent starts is detached from it, and what
// its Ready handed out still agrees with a fresh Ready once the parent has
// started.
func TestReadyAgreesOnAChildStoppedBeforeTheStart(t *testing.T) {
	s := di.New()
	c := s.Child("c")
	if err := c.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	early := c.Ready()
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fresh := isClosed(c.Ready()); isClosed(early) != fresh {
		t.Errorf("Ready asked before the start closed=%v, asked after closed=%v", isClosed(early), fresh)
	}
	if err := s.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReadyOnAChildStartedAlone(t *testing.T) {
	s := di.New()
	c := s.Child("c")
	g := c.Child("g")
	if err := c.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !isClosed(c.Ready()) || !isClosed(g.Ready()) {
		t.Error("Ready open below a started scope")
	}
	if isClosed(s.Ready()) {
		t.Error("Ready closed above the started scope")
	}
	if err := s.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A child started under a running root follows its own Start: a server in it
// does not serve when a later service in the child fails to start.
func TestReadyFollowsTheNearestStart(t *testing.T) {
	s := di.New()
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := s.Child("c")
	waiting := make(chan struct{})
	var served atomic.Bool
	c.Provide(func(*di.Scope) *readyServer { return &readyServer{} }).
		Eager().
		Go(func(ctx context.Context, _ *readyServer) error {
			close(waiting)
			select {
			case <-c.Ready():
				served.Store(true)
			case <-ctx.Done():
			}
			return nil
		})
	c.Provide(func(*di.Scope) *readyLater { return &readyLater{} }).
		Eager().
		OnStart(func(context.Context, *readyLater) error {
			<-waiting
			return errors.New("boom")
		})
	if err := c.Start(t.Context()); err == nil {
		t.Fatal("child Start succeeded")
	}
	if served.Load() {
		t.Error("worker served after its scope's Start failed")
	}
	if isClosed(c.Ready()) || isClosed(c.Child("g").Ready()) {
		t.Error("Ready closed below a failed Start")
	}
	if !isClosed(s.Ready()) {
		t.Error("Ready open on the started root")
	}
	if err := s.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestReadyUnderRun(t *testing.T) {
	s := di.New()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		<-s.Ready()
		cancel()
	}()
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReadyFailedRunLeavesItOpen(t *testing.T) {
	s := di.New()
	s.Provide(func(*di.Scope) *readyLater { return &readyLater{} }).
		Eager().
		OnStart(func(context.Context, *readyLater) error { return errors.New("boom") })
	if err := s.Run(t.Context()); err == nil {
		t.Fatal("Run succeeded")
	}
	if isClosed(s.Ready()) {
		t.Error("Ready closed by a failed Run")
	}
}

// Waiters asking concurrently with Start, on the scope and on children made
// meanwhile, are all woken.
func TestReadyConcurrentWithStart(t *testing.T) {
	for range 200 {
		s := di.New()
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				sc := s
				if i%2 == 1 {
					sc = s.Child("c").Child("g")
				}
				select {
				case <-sc.Ready():
				case <-time.After(5 * time.Second):
					t.Error("Ready never closed")
				}
			})
		}
		if err := s.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
		if err := s.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}
