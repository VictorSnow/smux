package smux

import (
	"strconv"
)

type Msg struct {
	ConnId  uint64
	MsgType uint32
	Length  uint32
	Buff    []byte
}

func (msg *Msg) String() string {
	return "connId=" + strconv.Itoa(int(msg.ConnId)) +
		" msgType=" + strconv.Itoa(int(msg.MsgType)) +
		" length=" + strconv.Itoa(int(msg.Length)) //+
	//" content=" + string(msg.Buff[:msg.Length])
}
