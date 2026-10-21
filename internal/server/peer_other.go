//go:build !linux

package server

import "net"

// peerUID is not implemented on this platform; the socket's file mode is
// the only access control.
func peerUID(net.Conn) (int, bool) { return 0, false }

// PeerCheckSupported reports whether peerUID works here.
const PeerCheckSupported = false
