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
