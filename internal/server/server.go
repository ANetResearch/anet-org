// Package server is the Unix-socket HTTP front of anet-org.
//
// The anet daemon's service module POSTs each call to /v1/<capability> with
// the arguments as a JSON object. Before anything is routed, the bearer
// token is compared in constant time; without it every path answers 401.
// The caller is taken from X-ANet-Caller, which the daemon sets only for a
// call it authenticated; a call without it is refused unless the operator
// enabled the admin-local mode. Replies are JSON objects; refusals use the
// service module's error shape, {"error": "<code>"} with a 4xx status.
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ANetResearch/anet-org/internal/config"
	"github.com/ANetResearch/anet-org/internal/org"
)

// Version is set by the build.
var Version = "0.1.0"

// Header names set by the anet service module.
const (
	HeaderCaller     = "X-ANet-Caller"
	HeaderCall       = "X-ANet-Call"
	HeaderCapability = "X-ANet-Capability"
	// HeaderVia is "agent" for a call that the organisation's agent makes
	// for the member named in X-ANet-Caller (anet's CallServiceAs).
	HeaderVia = "X-ANet-Via"
)

// MaxBodyBytes bounds a request body. The daemon's own max_args_bytes is
// lower for every capability gen-config declares.
const MaxBodyBytes = 64 << 10

// Server handles calls for one Service.
type Server struct {
	svc       *org.Service
	tokenHash [32]byte
	log       *slog.Logger
	sem       chan struct{}
	peers     map[int]bool // uids allowed to connect; nil disables the check
}

// New returns a server that requires token.
func New(svc *org.Service, token string, maxConcurrent int, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if maxConcurrent <= 0 {
		maxConcurrent = config.DefaultMaxConcurrent
	}
	return &Server{svc: svc, tokenHash: sha256.Sum256([]byte(token)), log: log, sem: make(chan struct{}, maxConcurrent)}
}

// SetPeerUIDs restricts connections to processes of these uids (checked
// with SO_PEERCRED where the platform supports it).
func (s *Server) SetPeerUIDs(uids []int) {
	s.peers = map[int]bool{}
	for _, u := range uids {
		s.peers[u] = true
	}
}

type ctxKey int

const peerKey ctxKey = 0

// ConnContext records the peer credentials of a connection.
func (s *Server) ConnContext(ctx context.Context, c net.Conn) context.Context {
	if uid, ok := peerUID(c); ok {
		return context.WithValue(ctx, peerKey, uid)
	}
	return context.WithValue(ctx, peerKey, -1)
}

// errorBody is the service module's error shape.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		b = []byte(`{"error":"internal_error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (s *Server) writeErr(w http.ResponseWriter, e *org.Error) {
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	s.writeJSON(w, e.Status, errorBody{Error: e.Code, Message: e.Message})
}

// tokenOK compares the presented bearer token with the configured one in
// constant time (both hashed first, so length is not compared either).
func (s *Server) tokenOK(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		tok = ""
	}
	got := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(got[:], s.tokenHash[:]) == 1 && ok && tok != ""
}

// ServeHTTP implements the protocol.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	capID := strings.TrimPrefix(r.URL.Path, "/v1/")
	status, code := 0, ""
	caller := r.Header.Get(HeaderCaller)
	via := r.Header.Get(HeaderVia)
	defer func() {
		// One line per call: no arguments, no reply, no message text.
		s.log.Info("call", "cap", logSafe(capID), "caller", logSafe(caller), "via", logSafe(via),
			"call_id", logSafe(r.Header.Get(HeaderCall)), "status", status, "code", code, "ms", time.Since(started).Milliseconds())
	}()
	refuse := func(e *org.Error) {
		status, code = e.Status, e.Code
		s.writeErr(w, e)
	}

	if s.peers != nil {
		uid, _ := r.Context().Value(peerKey).(int)
		if !s.peers[uid] {
			refuse(&org.Error{Status: http.StatusForbidden, Code: org.CodeUnauthorized, Message: "The connecting process is not permitted."})
			return
		}
	}
	// The token is checked before anything is routed, so an unauthenticated
	// request learns nothing, not even the version.
	if !s.tokenOK(r) {
		refuse(&org.Error{Status: http.StatusUnauthorized, Code: org.CodeUnauthorized, Message: "Authentication failed."})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
		status = http.StatusOK
		s.writeJSON(w, status, map[string]any{"status": "ok", "version": Version})
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/v1/") {
		refuse(&org.Error{Status: http.StatusNotFound, Code: org.CodeUnknownCapability, Message: "Calls are POST /v1/<capability>."})
		return
	}
	if !s.svc.Has(capID) {
		refuse(&org.Error{Status: http.StatusNotFound, Code: org.CodeUnknownCapability, Message: "Unknown capability."})
		return
	}
	if h := r.Header.Get(HeaderCapability); h != "" && h != capID {
		refuse(&org.Error{Status: http.StatusBadRequest, Code: org.CodeCapabilityMismatch,
			Message: "The capability header does not match the request path."})
		return
	}
	c := org.Caller{AID: caller, Via: via}
	if caller == "" {
		c.Local = true
	} else if !config.ValidAID(caller) {
		refuse(&org.Error{Status: http.StatusBadRequest, Code: org.CodeBadCaller, Message: "The caller header is malformed."})
		return
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		w.Header().Set("Retry-After", "5")
		refuse(&org.Error{Status: http.StatusServiceUnavailable, Code: org.CodeBusy, Message: "The organisation service is busy."})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		refuse(&org.Error{Status: http.StatusBadRequest, Code: org.CodeTooLong, Message: "The request body is too large."})
		return
	}
	args, derr := DecodeBody(body)
	if derr != nil {
		refuse(derr)
		return
	}
	res, err := s.svc.Dispatch(r.Context(), capID, c, args)
	if err != nil {
		e := org.AsError(err)
		if e == nil {
			s.log.Error("unhandled error", "cap", capID, "err", err.Error())
			e = &org.Error{Status: http.StatusInternalServerError, Code: org.CodeInternal, Message: "Internal error."}
		}
		refuse(e)
		return
	}
	status = http.StatusOK
	s.writeJSON(w, status, res)
}

// DecodeBody reads the request body: the arguments object, or the files
// envelope {"args": {...}, "files": [...]}, which no capability of this
// program accepts. An empty body is an empty object.
func DecodeBody(body []byte) (json.RawMessage, *org.Error) {
	t := bytes.TrimSpace(body)
	if len(t) == 0 {
		return json.RawMessage("{}"), nil
	}
	if t[0] != '{' || !json.Valid(t) {
		return nil, &org.Error{Status: http.StatusBadRequest, Code: org.CodeBadJSON, Message: "The arguments must be a JSON object."}
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(t, &top); err != nil {
		return nil, &org.Error{Status: http.StatusBadRequest, Code: org.CodeBadJSON, Message: "The arguments must be a JSON object."}
	}
	if f, ok := top["files"]; ok {
		if _, hasArgs := top["args"]; hasArgs && len(top) == 2 && len(bytes.TrimSpace(f)) > 0 && bytes.TrimSpace(f)[0] == '[' {
			return nil, &org.Error{Status: http.StatusBadRequest, Code: org.CodeFilesNotAccepted,
				Message: "This capability does not accept files."}
		}
	}
	return json.RawMessage(t), nil
}

// logSafe bounds and cleans a header value for the log.
func logSafe(s string) string {
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// Listen opens the Unix socket: its directory is created private if
// missing and must not be writable by other users; a stale socket nobody
// listens on is removed; the socket is made 0600, or 0660 with group.
func Listen(path, group string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return nil, fmt.Errorf("the socket directory %s is writable by every user; use a private directory", dir)
	}
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another process listens on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			l.Close()
			return nil, err
		}
		gid, _ := strconv.Atoi(g.Gid)
		if err := os.Chown(path, -1, gid); err != nil {
			l.Close()
			return nil, err
		}
		mode = 0o660
	}
	if err := os.Chmod(path, mode); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// ResolveUsers turns names or numeric uids into uids.
func ResolveUsers(list []string) ([]int, error) {
	var out []int
	for _, v := range list {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			out = append(out, n)
			continue
		}
		u, err := user.Lookup(v)
		if err != nil {
			return nil, fmt.Errorf("peer_users: %w", err)
		}
		n, _ := strconv.Atoi(u.Uid)
		out = append(out, n)
	}
	return out, nil
}

// HTTPServer wraps s with timeouts.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Handler:           s,
		ConnContext:       s.ConnContext,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          nil,
	}
}
