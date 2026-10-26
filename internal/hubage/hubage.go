// Package hubage asks a hub since when it has known an identity, for joins
// without an invite code (open_join).
//
// The hub's public registry entry (GET <hub>/agents/<aid>) carries
// registered_at, the UTC day of the identity's first registration as
// midnight. The registration itself happened at some time during that day,
// so an identity is known to be at least d old only from that midnight plus
// one day plus d.
package hubage

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxBody bounds the registry entry read from the hub (it carries reviews).
const maxBody = 1 << 20

// Errors a lookup returns besides transport failures.
var (
	// ErrNotRegistered: the hub does not list the identity as registered on
	// it (unknown, known only from a peer hub, or departed).
	ErrNotRegistered = errors.New("hubage: the identity is not registered on the hub")
)

// Checker looks identities up on one hub.
type Checker struct {
	hub    *url.URL
	client *http.Client
}

// New returns a Checker for hub (an https URL). When address is not empty,
// every connection goes to that host:port, or to the Unix socket of
// "unix:<path>" (a proxy to the hub), instead of the hub's resolved name;
// the certificate is still checked against the hub's name.
func New(hub, address string) (*Checker, error) {
	u, err := url.Parse(hub)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("hubage: %q is not an https URL", hub)
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, ServerName: u.Hostname()},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if p, ok := strings.CutPrefix(address, "unix:"); ok {
				return d.DialContext(ctx, "unix", p)
			}
			if address != "" {
				addr = address
			}
			return d.DialContext(ctx, network, addr)
		},
	}
	return &Checker{hub: u, client: &http.Client{Transport: tr, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// entry is the part of the hub's registry entry that is read.
type entry struct {
	Agent struct {
		AID          string `json:"aid"`
		Registered   bool   `json:"registered"`
		RegisteredAt string `json:"registered_at"`
		Departed     bool   `json:"departed"`
		HomeHub      string `json:"home_hub"`
	} `json:"agent"`
}

// KnownSince returns the earliest time at which the identity is certain to
// have been registered on the hub: the end of the UTC day the hub names as
// its first registration.
func (c *Checker) KnownSince(ctx context.Context, aid string) (time.Time, error) {
	if aid == "" || strings.ContainsAny(aid, "/?#%") {
		return time.Time{}, ErrNotRegistered
	}
	u := *c.hub
	u.Path = "/agents/" + url.PathEscape(aid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return time.Time{}, err
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return time.Time{}, ErrNotRegistered
	case res.StatusCode != http.StatusOK:
		return time.Time{}, fmt.Errorf("hubage: the hub answered %d", res.StatusCode)
	case len(body) > maxBody:
		return time.Time{}, errors.New("hubage: the hub's answer is too large")
	}
	var e entry
	if err := json.Unmarshal(body, &e); err != nil {
		return time.Time{}, fmt.Errorf("hubage: the hub's answer is not a registry entry: %w", err)
	}
	a := e.Agent
	if a.AID != aid || !a.Registered || a.Departed || a.HomeHub != "" {
		return time.Time{}, ErrNotRegistered
	}
	day, err := time.Parse(time.RFC3339, a.RegisteredAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("hubage: the hub gave no registration day: %w", err)
	}
	// The hub writes the day as UTC midnight; any other form could move the
	// day and is refused rather than interpreted.
	if _, off := day.Zone(); off != 0 || day.Hour() != 0 || day.Minute() != 0 || day.Second() != 0 || day.Nanosecond() != 0 {
		return time.Time{}, fmt.Errorf("hubage: the hub's registration day %q is not a UTC midnight", a.RegisteredAt)
	}
	day = day.UTC()
	return day.Add(24 * time.Hour), nil
}
