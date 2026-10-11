package dihttp

import (
	"crypto/tls"
	"net/http"
	"testing"
)

// An empty Addr binds the scheme's port, as ListenAndServe does.
func TestListenAddr(t *testing.T) {
	for _, c := range []struct {
		name string
		srv  *http.Server
		want string
	}{
		{"addr", &http.Server{Addr: "127.0.0.1:8080"}, "127.0.0.1:8080"},
		{"http", &http.Server{}, ":http"},
		{"https", &http.Server{TLSConfig: &tls.Config{}}, ":https"},
	} {
		if got := listenAddr(c.srv); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
