package smux

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

type Conn struct {
	connId   uint64    // 当前链接ID
	recvChan chan *Msg // 接收通道

	closeChan chan int
	state     int64 // 当前连接状态
	mu        sync.Mutex

	recvBuff  *WaitableBuff // 接收缓存
	timeAlive time.Duration // 生存时间

	dialChan chan bool
	s        *Smux
}

func NewConn(connId uint64, s *Smux) *Conn {

	conn := &Conn{
		connId:    connId,
		recvChan:  make(chan *Msg, 1024),
		closeChan: make(chan int),
		state:     STATE_ACTIVE,
		mu:        sync.Mutex{},
		recvBuff:  NewWaitableBuff(),
		timeAlive: time.Second * 120,
		dialChan:  make(chan bool),
		s:         s}

	go conn.loop()
	return conn
}

func (c *Conn) loop() {
	for {
		select {
		case msg := <-c.recvChan:
			if msg == nil {
				return
			}

			switch msg.MsgType {
			case MSG_CONN:
				if msg.Length > 0 {
					c.recvBuff.Write(msg.Buff)
				}
			case MSG_CONNECT_SUCCESS:
				c.dialChan <- true
			case MSG_CONNECT_ERROR:
				c.dialChan <- false
				c.Close(false)
			}
		case i := <-c.closeChan:
			// need close remote
			c.Close(i == 0)
			return
		case <-time.After(c.timeAlive):
			c.Close(true)
			return
		}
	}
}

func (c *Conn) Close(closeRemote bool) {
	if atomic.LoadInt64(&c.state) == STATE_CLOSE {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if atomic.LoadInt64(&c.state) == STATE_CLOSE {
		return
	}

	atomic.SwapInt64(&c.state, STATE_CLOSE)

	c.s.connsMu.Lock()
	delete(c.s.conns, c.connId)
	c.s.connsMu.Unlock()

	if closeRemote {
		msg := &Msg{c.connId, MSG_CLOSE, 0, []byte{}}
		c.s.sendBox <- msg
	}

	c.recvBuff.Close()
	c.s = nil

	// 清理数据
	close(c.recvChan)
	close(c.closeChan)
	close(c.dialChan)
}

func (c *Conn) Write(buff []byte) (int, error) {
	// copy slice
	tbuff := make([]byte, len(buff))
	copy(tbuff, buff)

	msg := &Msg{c.connId, MSG_CONN, uint32(len(tbuff)), tbuff}

	if atomic.LoadInt64(&c.state) == STATE_ACTIVE {
		c.s.sendBox <- msg
		return len(tbuff), nil
	}
	return 0, errors.New("conn closed")
}

func (c *Conn) Read(buff []byte) (int, error) {
	return c.recvBuff.Read(buff)
}
