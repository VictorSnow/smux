package main

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
	return "connId=" + strconv.Itoa(int(msg.ConnId)) + " msgType=" + strconv.Itoa(int(msg.MsgType)) + " content=" + string(msg.Buff[:msg.Length])
}
