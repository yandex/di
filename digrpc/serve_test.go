package digrpc_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.yandex/di"
	"golang.yandex/di/digrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// checker answers Check, counting calls; with hold set, a call waits on it.
type checker struct {
	grpc_health_v1.UnimplementedHealthServer
	calls   atomic.Int32
	entered chan struct{}
	hold    chan struct{}
}

func (c *checker) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	c.calls.Add(1)
	if c.hold != nil {
		close(c.entered)
		<-c.hold
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// served registers a server for c, run by Serve on lis.
func served(app *di.Scope, lis net.Listener, c *checker) {
	digrpc.Serve(app.Wire[*grpc.Server](func() *grpc.Server {
		srv := grpc.NewServer()
		grpc_health_v1.RegisterHealthServer(srv, c)
		return srv
	}), func(context.Context) (net.Listener, error) { return lis, nil })
}

func client(t *testing.T, lis *bufconn.Listener) grpc_health_v1.HealthClient {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return grpc_health_v1.NewHealthClient(conn)
}

// closed reports whether lis refuses connections. A bufconn dial waits for an
// Accept, so an open listener nobody serves times out instead.
func closed(t *testing.T, lis *bufconn.Listener) bool {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	conn, err := lis.DialContext(ctx)
	if err == nil {
		_ = conn.Close()
	}
	return err != nil && ctx.Err() == nil
}

func TestServeServesOnceStarted(t *testing.T) {
	app := di.New()
	lis := bufconn.Listen(1 << 20)
	served(app, lis, &checker{})
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	res, err := client(t, lis).Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil || res.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("Check = %v, %v", res, err)
	}
	if err := app.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestServeListenFailureFailsStart(t *testing.T) {
	app := di.New()
	boom := errors.New("port taken")
	digrpc.Serve(app.Wire[*grpc.Server](func() *grpc.Server { return grpc.NewServer() }),
		func(context.Context) (net.Listener, error) { return nil, boom })
	if err := app.Start(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want %v", err, boom)
	}
}

type later struct{}

// A service started after the server fails: a call made meanwhile is not
// served, the start rolls back, and the listener is closed though serving
// never began.
func TestServeServesNothingFromAFailedStart(t *testing.T) {
	app := di.New()
	lis := bufconn.Listen(1 << 20)
	c := &checker{}
	served(app, lis, c)
	health := client(t, lis)
	boom := errors.New("boom")
	var answered atomic.Bool
	app.Wire[*later](func() *later { return &later{} }).
		Eager().
		OnStart(func(ctx context.Context, _ *later) error {
			// A server serving now answers well within the deadline.
			ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			if _, err := health.Check(ctx, &grpc_health_v1.HealthCheckRequest{}); err == nil {
				answered.Store(true)
			}
			return boom
		})
	if err := app.Start(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want %v", err, boom)
	}
	if answered.Load() || c.calls.Load() != 0 {
		t.Error("a call was served from a failed start")
	}
	if !closed(t, lis) {
		t.Error("the listener outlived the rollback")
	}
}

// Stop drains: a call in flight finishes before the server stops.
func TestServeDrainsCallsInFlight(t *testing.T) {
	app := di.New()
	lis := bufconn.Listen(1 << 20)
	c := &checker{entered: make(chan struct{}), hold: make(chan struct{})}
	served(app, lis, c)
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	health := client(t, lis)
	done := make(chan error, 1)
	go func() {
		_, err := health.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		done <- err
	}()
	select {
	case <-c.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never reached the server")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- app.Stop(t.Context()) }()
	// The drain has begun once the listener refuses connections.
	waitClosed(t, lis)
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned with a call in flight: %v", err)
	default:
	}
	close(c.hold)
	if err := <-done; err != nil {
		t.Errorf("in-flight call: %v", err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
}

// waitClosed waits for lis to refuse connections, failing after five seconds.
func waitClosed(t *testing.T, lis *bufconn.Listener) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !closed(t, lis) {
		if time.Now().After(deadline) {
			t.Fatal("the listener is still accepting")
		}
		time.Sleep(time.Millisecond)
	}
}

// A Stop whose context has already expired still stops the server: the drain
// hook's bound fires at once and cuts GracefulStop short.
func TestServeImpatientStopStillStops(t *testing.T) {
	app := di.New()
	lis := bufconn.Listen(1 << 20)
	served(app, lis, &checker{})
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_ = app.Stop(ctx)
	waitClosed(t, lis)
}

func TestServeRejectsANilListener(t *testing.T) {
	app := di.New()
	digrpc.Serve(app.Wire[*grpc.Server](func() *grpc.Server { return grpc.NewServer() }),
		func(context.Context) (net.Listener, error) { return nil, nil })
	if err := app.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded with no listener")
	}
}

// A listener returned with an error is closed, not leaked.
func TestServeClosesAListenerReturnedWithAnError(t *testing.T) {
	app := di.New()
	lis := bufconn.Listen(1 << 20)
	digrpc.Serve(app.Wire[*grpc.Server](func() *grpc.Server { return grpc.NewServer() }),
		func(context.Context) (net.Listener, error) { return lis, errors.New("half bound") })
	if err := app.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded")
	}
	if !closed(t, lis) {
		t.Error("the listener was leaked")
	}
}
