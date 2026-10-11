package di_test

// Branches the rest of the suite reaches by luck or not at all, each pinned
// by one test through the exported API. edges_internal_test.go has the ones
// only an unexported call reaches.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.yandex/di"
)

type (
	edgeA       struct{}
	edgeB       struct{}
	edgeMember  struct{}
	edgeNeeder  struct{}
	edgeMissing struct{}
)

// Binding.Scope is the scope a binding was registered through.
func TestBindingScope(t *testing.T) {
	app := di.New()
	if b := app.Value(&edgeA{}); b.Scope() != app {
		t.Errorf("Scope() = %p, want %p", b.Scope(), app)
	}
}

// A zero Need names itself, since a message may print one.
func TestZeroNeedString(t *testing.T) {
	if got := fmt.Sprint(di.Need{}); got != "the zero Need" {
		t.Errorf("got %q", got)
	}
}

// Modules names an unnamed key type as reflect does.
func TestModulesNamesAnUnnamedKey(t *testing.T) {
	app := di.New()
	app.Value(map[string]int{"a": 1})
	if got := app.Modules(); !strings.Contains(got, "map[string]int") {
		t.Errorf("Modules lists the map key:\n%s", got)
	}
}

// A start hook that resolves through its constructor's kept Scope is in
// flight, so a failure there is a wiring failure, and the start reports its
// cause rather than a panic.
func TestStartHookReportsANestedWiringFailure(t *testing.T) {
	app := di.New()
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var kept *di.Scope
	app.Provide(func(sc *di.Scope) *edgeA { kept = sc; return &edgeA{} }).
		OnStart(func(context.Context, *edgeA) error { kept.Get[*edgeMissing](); return nil })
	_, err := app.Resolve[*edgeA]()
	if !errors.Is(err, di.ErrNotProvided) || strings.Contains(err.Error(), "panic") {
		t.Fatalf("got %v, want the missing dependency as the cause", err)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A goroutine a constructor started resolves under that constructor's node,
// and keeps resolving after the constructor returned: the path walk stops at
// the finished node rather than calling what is above it a cycle. The
// channels fix the order, so the test is the same on every run.
func TestResolutionUnderAFinishedConstructor(t *testing.T) {
	app := di.New()
	entered, returned := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	app.Provide(func(sc *di.Scope) *edgeA {
		go func() { _, err := sc.Resolve[*edgeB](); result <- err }()
		<-entered
		return &edgeA{}
	})
	app.Provide(func(sc *di.Scope) *edgeB {
		close(entered)
		<-returned
		sc.Get[*edgeMember]()
		return &edgeB{}
	})
	app.Value(&edgeMember{})
	app.Get[*edgeA]()
	close(returned)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// A group parameter declared in a scope above a member does not declare that
// member: a build in the declaring scope would not read it.
func TestExplainGroupMemberBelowTheDeclaringScope(t *testing.T) {
	root := di.New()
	root.Wire[*edgeNeeder](func([]*edgeMember) *edgeNeeder { return &edgeNeeder{} }).
		Needs(di.AllOf[*edgeMember]())
	child := root.Child("child")
	child.Wire[*edgeMember](func() *edgeMember { return &edgeMember{} }).Group()
	if got := child.Explain[*edgeMember](); strings.Contains(got, "declared by") {
		t.Errorf("the root's need is reported as declaring the child's member:\n%s", got)
	}
}
