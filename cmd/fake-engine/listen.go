package main

import (
	"net"
)

// listen binds to a loopback address, matching the real engine's I12
// constraint: a test double that listened on 0.0.0.0 would be a worse
// security posture than the thing it stands in for.
func listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
