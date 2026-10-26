package hubage

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testChecker(t *testing.T, h http.HandlerFunc) *Checker {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	// The hub's name is checked against the certificate; the test server's
	// certificate names example.com, and every connection goes to its address.
	c, err := New("https://example.com", strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	tr := c.client.Transport.(*http.Transport)
	tr.TLSClientConfig = &tls.Config{ServerName: "example.com", RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	return c
}

func TestKnownSince(t *testing.T) {
	const aid = "bafyreiexample0000000000000000000000000000000000000000000001"
	c := testChecker(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agents/" + aid:
			w.Write([]byte(`{"agent":{"aid":"` + aid + `","registered":true,"registered_at":"2026-10-20T00:00:00Z"},"reviews":[]}`))
		case "/agents/peer":
			w.Write([]byte(`{"agent":{"aid":"peer","home_hub":"https://other"},"note":"known from a peer"}`))
		case "/agents/gone":
			w.Write([]byte(`{"agent":{"aid":"gone","registered":true,"departed":true,"registered_at":"2026-01-01T00:00:00Z"}}`))
		case "/agents/liar":
			w.Write([]byte(`{"agent":{"aid":"someone-else","registered":true,"registered_at":"2020-01-01T00:00:00Z"}}`))
		case "/agents/offset":
			w.Write([]byte(`{"agent":{"aid":"offset","registered":true,"registered_at":"2026-10-20T00:00:00+14:00"}}`))
		case "/agents/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	got, err := c.KnownSince(ctx, aid)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("known since %v, want %v", got, want)
	}
	for _, a := range []string{"unknown", "peer", "gone", "liar", "a/b", ""} {
		if _, err := c.KnownSince(ctx, a); !errors.Is(err, ErrNotRegistered) {
			t.Errorf("%q: %v, want ErrNotRegistered", a, err)
		}
	}
	for _, a := range []string{"broken", "offset"} {
		if _, err := c.KnownSince(ctx, a); err == nil || errors.Is(err, ErrNotRegistered) {
			t.Errorf("%s: %v", a, err)
		}
	}
}

func TestNewRefusesPlainHTTP(t *testing.T) {
	if _, err := New("http://hub.example.org", ""); err == nil {
		t.Fatal("plain http accepted")
	}
}

// A unix: address reaches the hub through a socket (a proxy on the host);
// TLS is still checked against the hub's name.
func TestUnixAddress(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "hub.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"agent":{"aid":"x","registered":true,"registered_at":"2026-10-20T00:00:00Z"}}`))
	}))
	srv.Listener = ln
	srv.StartTLS()
	t.Cleanup(srv.Close)
	c, err := New("https://example.com", "unix:"+sock)
	if err != nil {
		t.Fatal(err)
	}
	tr := c.client.Transport.(*http.Transport)
	tr.TLSClientConfig = &tls.Config{ServerName: "example.com", RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	if _, err := c.KnownSince(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	tr.TLSClientConfig = &tls.Config{ServerName: "hub.example.org", RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
	tr.CloseIdleConnections()
	if _, err := c.KnownSince(context.Background(), "x"); err == nil {
		t.Fatal("a certificate for another name was accepted")
	}
}
