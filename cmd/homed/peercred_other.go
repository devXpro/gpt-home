//go:build !darwin && !linux

package main

import (
	"net"
	"os"
)

// peerUID has no portable implementation here; the socket's 0600 mode and the 0700 data dir are the guard.
func peerUID(net.Conn) (int, error) { return os.Getuid(), nil }
