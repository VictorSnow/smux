package main

type Msg struct {
	MsgId   uint64
	MsgType uint32
	Length  uint32
	Buff    []byte
}
