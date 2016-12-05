package smux

import (
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Smux struct {
	conn  net.Conn
	state int64
	addr  string
	mode  string

	// timeout
	dailTimeout      time.Duration
	keepAliveTimeout time.Duration
	// id
	connId uint64
	idStep uint64
	// send msg chan
	sendBox chan *Msg
	// conn collection
	conns   map[uint64]*Conn
	connsMu sync.Mutex

	accepts chan uint64

	nodelay bool
}

func NewSmux(addr string, mode string) *Smux {
	connId := 1
	if mode == "server" {
		connId = 2
	}

	return &Smux{
		conn:             nil,
		state:            STATE_CLOSE,
		addr:             addr,
		mode:             mode,
		dailTimeout:      5 * time.Second,
		keepAliveTimeout: 10 * time.Second,
		connId:           uint64(connId),
		idStep:           2,
		sendBox:          make(chan *Msg, 2048),
		conns:            make(map[uint64]*Conn),
		connsMu:          sync.Mutex{},
		accepts:          make(chan uint64, 30),
		nodelay:          true,
	}
}

func (s *Smux) Start() {
	if s.mode == "server" {
		s.startServer()
	} else {
		s.startClient()
	}
}

func (s *Smux) startServer() {
	s.mode = "server"
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		errorLog("server listen error", s.addr, err)
		return
	}

	for {
		conn, err := l.Accept()
		if err != nil {
			errorLog("server accept error", err)
			return
		}
		if atomic.LoadInt64(&s.state) == STATE_ACTIVE {
			conn.Close()
			errorLog("server accept while active")
			continue
		}

		if atomic.LoadInt64(&s.state) == STATE_CLOSING {
			conn.Close()
			errorLog("server accept while closed")
			continue
		}

		s.conn = conn
		if s.nodelay {
			nodelay(s.conn)
		}

		go s.HandleLoop()
	}
}

func (s *Smux) Accept() *Conn {
	connId := <-s.accepts

	s.connsMu.Lock()
	defer s.connsMu.Unlock()
	if conn, ok := s.conns[connId]; ok {
		return conn
	}
	return nil
}

func (s *Smux) startClient() error {
	s.mode = "client"
	conn, err := net.DialTimeout("tcp", s.addr, s.dailTimeout)
	if err != nil {
		debugLog("dial server failed", err)
		return err
	}

	s.conn = conn
	if s.nodelay {
		nodelay(s.conn)
	}

	go s.HandleLoop()
	return nil
}

func (s *Smux) Dail() (*Conn, error) {
	if atomic.LoadInt64(&s.state) != STATE_ACTIVE {
		return nil, errors.New("remote not success connected")
	}

	connId := atomic.AddUint64(&s.connId, s.idStep)
	msg := &Msg{connId, MSG_CONNECT, 0, []byte{}}
	s.sendBox <- msg

	conn := NewConn(msg.ConnId, s)

	s.connsMu.Lock()
	s.conns[connId] = conn
	s.connsMu.Unlock()

	select {
	case isSuccess := <-conn.dialChan:
		if isSuccess {
			return conn, nil
		} else {
			return nil, errors.New("connect error")
		}
	case <-time.After(s.dailTimeout):
		conn.Close(false)
		return nil, errors.New("dail timeout")
	}

	return nil, errors.New("fail back")
}

func (s *Smux) HandleLoop() {
	// watch in case of error
	defer func() {
		if err := recover(); err != nil {
			errorLog(err)
			atomic.StoreInt64(&s.state, STATE_CLOSE)
		}
	}()

	atomic.StoreInt64(&s.state, STATE_ACTIVE)

	defer func() {
		atomic.StoreInt64(&s.state, STATE_CLOSING)
		s.conn.Close()

		// 关闭链接
		for _, v := range s.conns {
			v.Close(false)
		}
	L:
		for {
			select {
			case <-s.accepts:
			case <-s.sendBox:
			default:
				break L
			}
		}

		s.conns = make(map[uint64]*Conn)
		atomic.StoreInt64(&s.state, STATE_CLOSE)
	}()

	closeChan := make(chan int)
	defer close(closeChan)

	wg := sync.WaitGroup{}
	wg.Add(2)

	// 接受消息
	go func() {
		defer wg.Done()
		defer func() {
			closeChan <- 1
		}()

		for {
			msg, err := s.recvMsg()

			debugLog(s.mode, "recv msg", msg, err)

			if err != nil {
				if istimeout(err) {
					continue
				}
				return
			}

			switch msg.MsgType {
			case MSG_CONNECT:
				// 加入 conn
				conn := NewConn(msg.ConnId, s)
				s.accepts <- msg.ConnId

				s.connsMu.Lock()
				s.conns[msg.ConnId] = conn
				s.connsMu.Unlock()

				// 发送成功消息
				s.sendBox <- &Msg{msg.ConnId, MSG_CONNECT_SUCCESS, 0, []byte{}}
			case MSG_CONNECT_ERROR:
				fallthrough
			case MSG_CONNECT_SUCCESS:
				fallthrough
			case MSG_CONN:
				// 内容
				s.connsMu.Lock()
				if conn, ok := s.conns[msg.ConnId]; ok {
					conn.recvChan <- msg
				}
				s.connsMu.Unlock()

			case MSG_CLOSE:
				// 被动关闭
				s.connsMu.Lock()
				if conn, ok := s.conns[msg.ConnId]; ok {
					conn.closeChan <- 1
				}
				s.connsMu.Unlock()
			case MSG_KEEPALIVE:
				// ignore
			default:
				panic("unknow msg type" + strconv.Itoa(int(msg.MsgType)))
			}
		}
	}()

	// 发送消息
	go func() {
		defer wg.Done()
		for {

			select {
			case <-closeChan:
				return
			case msg := <-s.sendBox:
				err := s.sendMsg(msg)

				if err != nil {
					errorLog("send conn failed", err)
					return
				}
				// 如果是关闭连接
				if msg.MsgType == MSG_CLOSE {
					s.connsMu.Lock()
					delete(s.conns, msg.ConnId)
					s.connsMu.Unlock()
				}
			case <-time.After(s.keepAliveTimeout):
				if s.mode == "client" {
					msg := &Msg{0, MSG_KEEPALIVE, 0, []byte{}}
					s.sendBox <- msg
				}
			}
		}
	}()
	wg.Wait()
}

func (s *Smux) readFull(buff []byte) (int, error) {
	min := len(buff)
	m := 0

	if min == 0 {
		return 0, nil
	}

	for {
		n, err := s.conn.Read(buff)
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

func (s *Smux) recvMsg() (*Msg, error) {
	header := make([]byte, 16)
	_, err := s.readFull(header)
	if err != nil {
		return nil, err
	}

	msg := &Msg{}
	msg.ConnId = binary.BigEndian.Uint64(header)
	msg.MsgType = binary.BigEndian.Uint32(header[8:])
	msg.Length = binary.BigEndian.Uint32(header[12:])
	msg.Buff = make([]byte, msg.Length)

	if msg.Length > 0 {
		_, err = s.readFull(msg.Buff)
		if err != nil {
			return nil, err
		}
	}
	return msg, nil
}

func (s *Smux) sendMsg(msg *Msg) error {
	buff := make([]byte, msg.Length+16)
	binary.BigEndian.PutUint64(buff, msg.ConnId)
	binary.BigEndian.PutUint32(buff[8:], msg.MsgType)
	binary.BigEndian.PutUint32(buff[12:], msg.Length)
	if msg.Length > 0 {
		copy(buff[16:], msg.Buff)
	}
retry:
	n, err := s.conn.Write(buff)
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
