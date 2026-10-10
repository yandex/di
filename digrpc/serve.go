package digrpc

import (
	"context"
	"errors"
	"net"

	"golang.yandex/di"
	"google.golang.org/grpc"
)

// Serve runs the server b builds for as long as its scope does, on the
// listener listen returns. It makes the binding Eager and gives it every
// lifecycle hook, so add none of your own: a hook added before or after is
// rejected as a second one, named at the line in this package that added the
// other.
//
//	var lc net.ListenConfig
//	digrpc.Serve(app.Wire[*grpc.Server](NewServer),
//		func(ctx context.Context) (net.Listener, error) {
//			return lc.Listen(ctx, "tcp", ":50051")
//		})
//
// OnStart calls listen, so a busy port fails Start. Its context bounds the
// start, so it may bound the bind but must not be kept by the listener, which
// may be any, such as a bufconn in tests. A worker
// serves once the whole Start has succeeded (see [di.Scope.Ready]), so a start
// that rolls back serves no call; serving that stops with an error stops the
// application. OnDrain stops the server gracefully, letting calls in flight
// finish with their scopes, and cuts them short if its context expires first;
// OnStop stops it.
//
// A key names one value per scope, so a second server in the same scope is
// registered under a type of its own, one whose underlying type is
// *grpc.Server. A constructor returning *grpc.Server still serves it:
//
//	type AdminServer *grpc.Server
//
//	digrpc.Serve(app.Wire[AdminServer](NewAdminServer), listenAdmin)
func Serve[S ~*grpc.Server](b di.Binding[S], listen func(context.Context) (net.Listener, error)) di.Binding[S] {
	s := b.Scope()
	// An Eager binding is never Scoped, so it builds one server and this is
	// its listener, handed from OnStart to the worker the start step launches.
	var ln net.Listener
	return b.Eager().
		OnStart(func(ctx context.Context, _ S) (err error) {
			ln, err = listen(ctx)
			switch {
			case err != nil && ln != nil:
				_ = ln.Close()
			case err == nil && ln == nil:
				err = errors.New("digrpc: Serve: listen returned no listener and no error")
			}
			return err
		}).
		Go(func(ctx context.Context, served S) error {
			srv := (*grpc.Server)(served)
			// Serve closes the listener once it has it; this covers the paths
			// that never reach it.
			defer func() { _ = ln.Close() }()
			select {
			case <-s.Ready():
			case <-ctx.Done():
				return nil // the start failed and rolled back
			}
			// The drain stops the server before the worker is cancelled; this
			// stops it when a missed deadline skipped the drain.
			defer context.AfterFunc(ctx, srv.Stop)()
			// Serve returns nil after GracefulStop or Stop, and
			// ErrServerStopped when the drain stopped the server first.
			if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				return err
			}
			return nil
		}).
		OnDrain(func(ctx context.Context, served S) error {
			srv := (*grpc.Server)(served)
			// GracefulStop takes no context: Stop cuts the remaining calls
			// short if ctx expires first.
			stop := context.AfterFunc(ctx, srv.Stop)
			srv.GracefulStop()
			if !stop() {
				return ctx.Err()
			}
			return nil
		}).
		OnStop(func(_ context.Context, srv S) error { (*grpc.Server)(srv).Stop(); return nil })
}
