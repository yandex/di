// Package dihttp connects a di.Scope to net/http.
//
// A [Middleware] gives every request its own child scope holding the
// *http.Request, so services that depend on the request are declared once in
// the application scope as Scoped and built per request. Handlers reach the
// scope through [di.FromContext], or are made with [Handle], which resolves a
// handler type from that scope and calls one of its methods. [Module]
// registers the middleware as a service; [NewMiddleware] makes one directly.
// [Serve] runs an *http.Server the container builds for as long as its scope.
package dihttp

import (
	"context"
	"errors"
	"net"
	"net/http"

	"golang.yandex/di"
)

// Middleware gives every request its own child scope of the application
// scope: the *http.Request is registered in it, the scope is attached to the
// request context, and it is stopped and detached when the handler returns.
// Stop failures reach the application scope's observers as EventStop with Err
// set. It has the usual middleware shape, so it wraps a handler directly or
// goes into a router's Use.
type Middleware func(http.Handler) http.Handler

// Module registers a Middleware over the scope it is applied to, so that a
// constructor wired into that scope can take one as a parameter:
//
//	app.Use(dihttp.Module, api.Module)
//
//	func NewServer(cfg Config, mw dihttp.Middleware) *http.Server {
//		return &http.Server{Addr: cfg.Addr, Handler: mw(mux)}
//	}
//
// It is registered with Provide rather than Wire because the
// middleware needs the scope itself, to open a child per request.
func Module(s *di.Scope) {
	s.Provide(NewMiddleware)
}

// NewMiddleware makes a Middleware whose request scopes are children of s.
func NewMiddleware(s *di.Scope) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := s.Child("request")
			// The handler and the constructors must see one *http.Request:
			// routers write path values and the matched pattern into the
			// request they are given, so a copy registered here would miss
			// them.
			r = r.WithContext(di.WithScope(r.Context(), req))
			req.Value(r)
			defer func() { _ = req.Stop(context.WithoutCancel(r.Context())) }()
			next.ServeHTTP(w, r)
		})
	}
}

// Handle serves each request with a method of H, resolved from the request's
// scope. A method expression names both, so no type argument is needed:
//
//	mux.Handle("GET /users/{id}", dihttp.Handle((*Users).Show))
//
// H is a service like any other: declared Scoped when it needs the request,
// and once for the application when it does not. A wiring failure at request
// time panics with the error, which net/http recovers and logs with the
// request; Validate at startup is what keeps that from happening.
func Handle[H any](method func(H, http.ResponseWriter, *http.Request)) http.Handler {
	return HandleFunc(method)
}

// HandleFunc is Handle as an http.HandlerFunc, for routers whose route
// methods take one rather than an http.Handler; it resolves and fails as
// Handle does:
//
//	r.Get("/users/{id}", dihttp.HandleFunc((*Users).Show))
func HandleFunc[H any](method func(H, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := di.FromContext(r.Context())
		if !ok {
			panic("dihttp: no request scope on the context; is the Middleware above this handler?")
		}
		method(req.Get[H](), w, r)
	}
}

// Serve runs the server b builds for as long as its scope does. It makes the
// binding Eager and gives it every lifecycle hook, so add none of your own: a
// hook added before or after is rejected as a second one, named at the line
// in this package that added the other.
//
//	dihttp.Serve(app.Wire[*http.Server](NewServer))
//
// OnStart binds the listener, so a busy port fails Start; when Addr asks for
// port 0, the port chosen is written back into it. A worker serves once the
// whole Start has succeeded (see [di.Scope.Ready]), so a start that rolls back
// serves no request; serving that stops with an error stops the application.
// OnDrain shuts the server down gracefully, letting requests in flight finish
// with their scopes, and OnStop closes it. A server with a TLSConfig is served
// with ServeTLS, from the certificates there.
func Serve(b di.Binding[*http.Server]) di.Binding[*http.Server] {
	s := b.Scope()
	// An Eager binding is never Scoped, so it builds one server and this is
	// its listener, handed from OnStart to the worker the start step launches.
	var ln net.Listener
	return b.Eager().
		OnStart(func(ctx context.Context, srv *http.Server) (err error) {
			addr := srv.Addr
			if addr == "" {
				addr = ":http"
				if srv.TLSConfig != nil {
					addr = ":https"
				}
			}
			var lc net.ListenConfig
			if ln, err = lc.Listen(ctx, "tcp", addr); err != nil {
				return err
			}
			if host, port, err := net.SplitHostPort(addr); err == nil && port == "0" {
				_, port, _ = net.SplitHostPort(ln.Addr().String())
				srv.Addr = net.JoinHostPort(host, port)
			}
			return nil
		}).
		Go(func(ctx context.Context, srv *http.Server) error {
			// Serve closes the listener, but ServeTLS can fail before
			// reaching it, and nothing else would.
			defer func() { _ = ln.Close() }()
			select {
			case <-s.Ready():
			case <-ctx.Done():
				return nil // the start failed and rolled back
			}
			var err error
			if srv.TLSConfig != nil {
				err = srv.ServeTLS(ln, "", "")
			} else {
				err = srv.Serve(ln)
			}
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}).
		OnDrain(func(ctx context.Context, srv *http.Server) error { return srv.Shutdown(ctx) }).
		OnStop(func(_ context.Context, srv *http.Server) error { return srv.Close() })
}
