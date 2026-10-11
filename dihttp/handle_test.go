package dihttp_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.yandex/di/dihttp"
)

type users struct{}

func (*users) Show(http.ResponseWriter, *http.Request) {}

// A handler served without the Middleware above it has no scope to resolve
// from, and says so rather than failing inside the container.
func TestHandleWithoutAScopePanics(t *testing.T) {
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "no request scope") {
			t.Fatalf("got %q, want a panic naming the missing scope", msg)
		}
	}()
	dihttp.Handle((*users).Show).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
