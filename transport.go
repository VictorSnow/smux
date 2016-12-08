package smux

import (
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type transport struct {
	conn             net.Conn
	keepAliveTimeout time.Duration

	sendBox chan *Msg  // 发送箱
	onrecv  func(*Msg) // 接受消息回调
	onclose func()     // 关闭回调
	state   int32      // 链接是否关闭
}

func newTransport(conn net.Conn, onrecv func(*Msg), onclose func()) *transport {
	return &transport{
		conn:             conn,
		keepAliveTimeout: 20 * time.Second,
		sendBox:          make(chan *Msg, 50),
		onrecv:           onrecv,
		onclose:          onclose,
		state:            STATE_ACTIVE,
	}
}

/**
* 多路复用主流程
 */
func (t *transport) handle() {
	wg := sync.WaitGroup{}
	wg.Add(2)

	once := sync.Once{}

	go func() {
		defer wg.Done()
		defer once.Do(t.close)

		t.sendHandle()
	}()
	go func() {
		defer wg.Done()
		defer once.Do(t.close)

		t.recvHandle()
	}()
	wg.Wait()
}

func (t *transport) close() {
	if atomic.SwapInt32(&t.state, STATE_CLOSE) == STATE_CLOSE {
		return
	}
	t.onclose()
	close(t.sendBox)
	t.conn.Close()
}

/**
* 处理消息的接受和传递
 */
func (t *transport) sendHandle() {
	// 发送消息
	for atomic.LoadInt32(&t.state) == STATE_ACTIVE {
		select {
		case msg := <-t.sendBox:
			if msg == nil {
				return
			}
			t.sendMsg(msg)
		case <-time.After(t.keepAliveTimeout):
			// send keepalive
			msg := &Msg{0, MSG_KEEPALIVE, 0, []byte{}}
			err := t.sendMsg(msg)
			if err != nil {
				return
			}
		}
	}
}

func (t *transport) recvHandle() {
	for atomic.LoadInt32(&t.state) == STATE_ACTIVE {
		msg, err := t.recvMsg()
		if err != nil {
			debugLog("transport recv error", err)
			return
		}
		t.onrecv(msg)
	}
}

func (t *transport) readFull(buff []byte) (int, error) {
	min := len(buff)
	m := 0

	if min == 0 {
		return 0, nil
	}

	for {
		n, err := t.conn.Read(buff)
		if err != nil && !istimeout(err) {
			return 0, err
		}
		m += n
		if m >= min {
			return m, nil
		} else {
			buff = buff[n:]
		}
	}
	return 0, errors.New("unexcept end")
}

func (t *transport) recvMsg() (*Msg, error) {
	header := make([]byte, 16)
	_, err := t.readFull(header)
	if err != nil {
		return nil, err
	}

	msg := &Msg{}
	msg.ConnId = binary.BigEndian.Uint64(header)
	msg.MsgType = binary.BigEndian.Uint32(header[8:])
	msg.Length = binary.BigEndian.Uint32(header[12:])
	msg.Buff = make([]byte, msg.Length)

	if msg.Length > 0 {
		_, err = t.readFull(msg.Buff)
		if err != nil {
			return nil, err
		}
	}
	return msg, nil
}

func (t *transport) sendMsg(msg *Msg) error {
	buff := make([]byte, msg.Length+16)
	binary.BigEndian.PutUint64(buff, msg.ConnId)
	binary.BigEndian.PutUint32(buff[8:], msg.MsgType)
	binary.BigEndian.PutUint32(buff[12:], msg.Length)
	if msg.Length > 0 {
		copy(buff[16:], msg.Buff)
	}
retry:
	n, err := t.conn.Write(buff)
	if err != nil {
		if istimeout(err) {
			if n != 0 {
				panic("unexcepted timeout send")
			}
			goto retry
		}

		return err
	}
	if n != len(buff) {
		return errors.New("content not match")
	}
	return nil
}
