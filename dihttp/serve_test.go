package dihttp_test

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.yandex/di"
	"golang.yandex/di/dihttp"
)

func get(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	body, err := fetch(c, url)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func fetch(c *http.Client, url string) (string, error) {
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func hello(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "hello") }

func TestServeServesOnceStarted(t *testing.T) {
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(hello)}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	srv := app.Get[*http.Server]()
	if got := get(t, http.DefaultClient, "http://"+srv.Addr); got != "hello" {
		t.Errorf("got %q", got)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServeBusyPortFailsStart(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: ln.Addr().String(), Handler: http.HandlerFunc(hello)}
	}))
	if err := app.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded on a busy port")
	}
}

type later struct{}

// A service started after the server fails: a request already waiting on the
// bound port is never answered, and the start rolls back.
func TestServeServesNothingFromAFailedStart(t *testing.T) {
	app := di.New()
	var served, answered atomic.Bool
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			served.Store(true)
		})}
	}))
	boom := errors.New("boom")
	app.Wire[*later](func() *later { return &later{} }).
		Eager().
		OnStart(func(context.Context, *later) error {
			conn, err := net.Dial("tcp", app.Get[*http.Server]().Addr)
			if err != nil {
				return err
			}
			defer func() { _ = conn.Close() }()
			// Connected, so the request is waiting on the port: a server
			// serving now answers it well within the deadline.
			if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
				return err
			}
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			if n, _ := conn.Read(make([]byte, 1)); n > 0 {
				answered.Store(true)
			}
			return boom
		})
	if err := app.Start(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want %v", err, boom)
	}
	if served.Load() || answered.Load() {
		t.Error("a request was served from a failed start")
	}
}

// Stop drains: a request in flight finishes before the server closes.
func TestServeDrainsRequestsInFlight(t *testing.T) {
	app := di.New()
	entered, release := make(chan struct{}), make(chan struct{})
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-release
			hello(w, r)
		})}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	addr := app.Get[*http.Server]().Addr
	got := make(chan string, 1)
	go func() {
		body, err := fetch(http.DefaultClient, "http://"+addr)
		got <- cmp.Or(body, fmt.Sprint(err))
	}()
	<-entered
	stopped := make(chan error, 1)
	go func() { stopped <- app.Stop(t.Context()) }()
	// The drain has begun once the listener refuses connections.
	for {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			break
		}
		_ = c.Close()
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned with a request in flight: %v", err)
	default:
	}
	close(release)
	if body := <-got; body != "hello" {
		t.Errorf("in-flight request got %q", body)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

func TestServeTLS(t *testing.T) {
	ts := httptest.NewTLSServer(nil)
	cfg, client := ts.TLS.Clone(), ts.Client()
	ts.Close()
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(hello), TLSConfig: cfg}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := get(t, client, "https://"+app.Get[*http.Server]().Addr); got != "hello" {
		t.Errorf("got %q", got)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Serving that stops with an error stops the application: a TLSConfig with
// no certificate makes ServeTLS fail at once, and Run returns its error.
func TestServeFailureStopsRun(t *testing.T) {
	app := di.New()
	var srv *http.Server
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		srv = &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(hello), TLSConfig: &tls.Config{}}
		return srv
	}))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := app.Run(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("Run = %v (context %v), want the serve error", err, ctx.Err())
	}
	// ServeTLS failed before Serve took the listener; the port is free.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatalf("the listener outlived Run: %v", err)
	}
	_ = ln.Close()
}

// Only a port of 0 is written back: the host stays as configured.
func TestServeKeepsTheHostOfAddr(t *testing.T) {
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "localhost:0", Handler: http.HandlerFunc(hello)}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	addr := app.Get[*http.Server]().Addr
	if host, port, _ := net.SplitHostPort(addr); host != "localhost" || port == "0" {
		t.Errorf("Addr = %q, want localhost and the port chosen", addr)
	}
	if got := get(t, http.DefaultClient, "http://"+addr); got != "hello" {
		t.Errorf("got %q", got)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// A Stop whose context has already expired still stops the server.
func TestServeImpatientStopStillStops(t *testing.T) {
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(hello)}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	addr := app.Get[*http.Server]().Addr
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = app.Stop(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			return
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("the server is still accepting")
		}
		time.Sleep(time.Millisecond)
	}
}

// adminServer is a second key for an *http.Server in the same scope.
type adminServer *http.Server

// A second server in one scope is registered under a type of its own and
// served by the same Serve; both serve from the one Start and stop with it.
func TestServeASecondKey(t *testing.T) {
	app := di.New()
	dihttp.Serve(app.Wire[*http.Server](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(hello)}
	}))
	dihttp.Serve(app.Wire[adminServer](func() *http.Server {
		return &http.Server{Addr: "127.0.0.1:0", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "admin")
		})}
	}))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	user := app.Get[*http.Server]().Addr
	admin := (*http.Server)(app.Get[adminServer]()).Addr
	if user == admin {
		t.Fatalf("both servers on %s", user)
	}
	if got := get(t, http.DefaultClient, "http://"+user); got != "hello" {
		t.Errorf("user server answered %q", got)
	}
	if got := get(t, http.DefaultClient, "http://"+admin); got != "admin" {
		t.Errorf("admin server answered %q", got)
	}
	// The second key is reported under its own name, not as the server it
	// points at, or two servers would read as one.
	const name = "golang.yandex/di/dihttp_test.adminServer"
	if got := app.Explain[adminServer](); !strings.HasPrefix(got, name) {
		t.Errorf("Explain names the admin server:\n%s\nwant it to start with %s", got, name)
	}
	if got := app.Modules(); !strings.Contains(got, "dihttp_test.adminServer") {
		t.Errorf("Modules lists the admin server:\n%s", got)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, addr := range []string{user, admin} {
		if _, err := fetch(http.DefaultClient, "http://"+addr); err == nil {
			t.Errorf("%s still serves after Stop", addr)
		}
	}
}
