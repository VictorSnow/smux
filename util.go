package smux

import (
	"net"
)

func istimeout(e error) bool {
	if ne, ok := e.(net.Error); ok && ne.Timeout() {
		return true
	}
	return false
}

func nodelay(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
}
