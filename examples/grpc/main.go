// Graceful shutdown of a gRPC server, with a service built per call.
//
// The digrpc interceptor opens a scope for every call and registers the
// *digrpc.Call in it, and digrpc.Register serves the health service with an
// implementation resolved from that scope, so a Scoped Health is built per
// call with a Caller read from the call's metadata. digrpc.Serve gives the
// server its lifecycle: it binds in OnStart, serves once the whole start has
// succeeded, and drains with GracefulStop, which stops accepting calls and
// waits for in-flight ones. Run starts the scope, waits for SIGINT/SIGTERM or
// a Shutdown call, then stops everything in reverse order with a bounded
// context. Draining runs before anything is torn down, so those calls still
// have their scopes.
package main

import (
	"cmp"
	"context"
	"log"
	"net"
	"strings"
	"time"

	"golang.yandex/di"
	"golang.yandex/di/digrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

type DB struct{ dsn string }

// Caller is who is making the call, read from its metadata. It depends on
// *digrpc.Call, which only a call scope provides, so it is Scoped.
type Caller struct{ Name string }

func NewCaller(c *digrpc.Call) *Caller {
	md, _ := metadata.FromIncomingContext(c.Context)
	return &Caller{Name: cmp.Or(strings.Join(md.Get("x-caller"), ","), "anonymous")}
}

// Health is the service implementation, built per call because it takes the
// Caller. Methods it does not define fall through to the embedded type.
type Health struct {
	grpc_health_v1.UnimplementedHealthServer
	db     *DB
	caller *Caller
}

func NewHealth(db *DB, c *Caller) *Health { return &Health{db: db, caller: c} }

func (h *Health) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	time.Sleep(2 * time.Second) // simulate slow work that must not be cut short
	log.Println("checked by", h.caller.Name, "against", h.db.dsn)
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// NewServer is a plain constructor: the interceptor arrives as a dependency,
// and Register resolves *Health from each call's scope.
func NewServer(ic digrpc.Interceptor) *grpc.Server {
	srv := grpc.NewServer(ic.Options()...)
	digrpc.Register[*Health](srv, &grpc_health_v1.Health_ServiceDesc)
	return srv
}

func main() {
	app := di.New()
	app.Use(digrpc.Module)

	app.Wire[*DB](func() *DB { return &DB{dsn: "postgres://localhost/app"} }).
		OnStop(func(ctx context.Context, db *DB) error { log.Println("db closed"); return nil })
	app.Wire[*Caller](NewCaller).Scoped()
	app.Wire[*Health](NewHealth).Scoped()

	// Binds :50051 in OnStart, so a busy port fails Start; serves after Ready.
	var lc net.ListenConfig
	digrpc.Serve(app.Wire[*grpc.Server](NewServer), func(ctx context.Context) (net.Listener, error) {
		ln, err := lc.Listen(ctx, "tcp", ":50051")
		if err == nil {
			log.Println("listening on", ln.Addr())
		}
		return ln, err
	})

	// The graph is checked as a call scope would resolve it: the interceptor
	// is provided by the module, the call by each call.
	if err := app.Validate(di.Provided[*digrpc.Call]()).Err(); err != nil {
		log.Fatal(err)
	}

	// Blocks until Ctrl-C, SIGTERM, or app.Shutdown. A second signal cancels
	// the stop context so a hung hook cannot keep the process alive.
	if err := app.Run(context.Background(), di.StopTimeout(10*time.Second)); err != nil {
		log.Fatal(err)
	}
}
