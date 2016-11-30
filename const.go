package smux

const (
	MSG_CONNECT         = 1
	MSG_CONNECT_SUCCESS = 2
	MSG_CONNECT_ERROR   = 3
	MSG_CONN            = 4
	MSG_CLOSE           = 5
	MSG_KEEPALIVE       = 6

	STATE_ACTIVE  = 1
	STATE_CLOSE   = 2
	STATE_CLOSING = 3
)
