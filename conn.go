package smux

import (
	"bytes"
	"errors"
	"io"
	"sync/atomic"
)

type Conn struct {
	connId   uint64    // 当前链接ID
	recvChan chan *Msg // 接收通道
	sendBox  chan *Msg // 发送通道

	closeChan chan int
	state     int64 // 当前连接状态

	recvBuff  bytes.Buffer // 接收缓存
	buffState chan int     // 缓存是否有内容
}

func NewConn(connId uint64, sendBox chan *Msg) *Conn {
	return &Conn{
		connId:    connId,
		recvChan:  make(chan *Msg, 20),
		sendBox:   sendBox,
		closeChan: make(chan int),
		state:     STATE_ACTIVE,
		recvBuff:  bytes.Buffer{},
		buffState: make(chan int)}
}

func (c *Conn) loop() {
	defer close(c.recvChan)
	defer close(c.closeChan)
	defer close(c.buffState)

	for {
		select {
		case i := <-c.closeChan:
			atomic.SwapInt64(&c.state, STATE_CLOSE)
			if i == 0 {
				msg := &Msg{c.connId, MSG_CLOSE, 0, []byte{}}
				c.sendBox <- msg
			}
			break
		case msg := <-c.recvChan:
			switch msg.MsgType {
			case MSG_CONN:
				debugLog("conn recv", string(msg.Buff[:msg.Length]))
				c.notifyBuffer()
				c.recvBuff.Write(msg.Buff)
			}
		}
	}
}

func (c *Conn) Close() error {
	select {
	case c.closeChan <- 0:
		return nil
	default:
		return errors.New("close fail")
	}
}

func (c *Conn) Send(buff []byte) (int, error) {
	msg := &Msg{c.connId, MSG_CONN, uint32(len(buff)), buff}
	if atomic.LoadInt64(&c.state) == STATE_ACTIVE {
		c.sendBox <- msg
		return len(buff), nil
	}
	return 0, errors.New("conn closed")
}

func (c *Conn) Recv(buff []byte) (int, error) {
	if atomic.LoadInt64(&c.state) == STATE_ACTIVE {
	retry:
		n, err := c.recvBuff.Read(buff)
		if err == io.EOF {
			c.waitBuffer()
			goto retry
		}

		return n, err
	}
	return 0, errors.New("conn closed")
}

func (c *Conn) notifyBuffer() {
	select {
	case c.buffState <- 1:
	}
}

func (c *Conn) waitBuffer() {
	<-c.buffState
}
