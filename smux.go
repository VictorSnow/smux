package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Msg struct {
	MsgId   uint64
	MsgType uint32
	Length  uint32
	Buff    []byte
}

type Smux struct {
	conn  net.Conn
	state int64
	addr  string
	mode  string

	// timeout
	dailTimeout      time.Duration
	keepAliveTimeout time.Duration

	connId uint64

	sendBox chan *Msg

	conns   map[uint64]*Conn
	connsMu sync.Mutex

	accepts chan uint64
}

func NewSmux(addr string) *Smux {
	return &Smux{
		conn:             nil,
		state:            STATE_CLOSE,
		addr:             addr,
		dailTimeout:      5 * time.Second,
		keepAliveTimeout: 10 * time.Second,
		connId:           1,
		sendBox:          make(chan *Msg, 2048),
		conns:            make(map[uint64]*Conn),
		connsMu:          sync.Mutex{},
		accepts:          make(chan uint64, 30),
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
		return err
	}

	s.conn = conn
	go s.HandleLoop()
	return nil
}

func (s *Smux) Dail() (*Conn, error) {
	connId := atomic.AddUint64(&s.connId, 1)
	msg := &Msg{connId, MSG_CONNECT, 0, []byte{}}
	s.sendBox <- msg

	conn := &Conn{connId, make(chan *Msg, 20), s.sendBox, make(chan int), STATE_ACTIVE, bytes.Buffer{}, make(chan int)}

	s.connsMu.Lock()
	s.conns[connId] = conn
	s.connsMu.Unlock()

	go conn.loop()

	select {
	case msg := <-conn.recvChan:
		switch msg.MsgType {
		case MSG_CONNECT_ERROR:
			return nil, errors.New("connect error")
		case MSG_CONNECT_SUCCESS:
			return conn, nil
		}
	case <-time.After(s.dailTimeout):
		return nil, errors.New("dail timeout")
	}

	return nil, errors.New("fail back")
}

func (s *Smux) HandleLoop() {
	atomic.StoreInt64(&s.state, STATE_ACTIVE)

	defer func() {
		atomic.StoreInt64(&s.state, STATE_CLOSING)

		// clear sequence
		for {
			select {
			case <-s.accepts:
			case <-s.sendBox:
			default:
				break
			}
		}

		// 关闭链接
		for _, v := range s.conns {
			select {
			case v.closeChan <- 0:
			}
		}

		s.conns = make(map[uint64]*Conn)

		s.conn.Close()
		atomic.StoreInt64(&s.state, STATE_CLOSE)
	}()

	// 接受消息
	go func() {
		for {
			msg, err := s.recvMsg()

			log.Println("recv msg", msg, err)

			if err != nil {
				if istimeout(err) {
					continue
				}
				return
			}

			switch msg.MsgType {
			case MSG_CONNECT:
				// 加入 conn
				conn := &Conn{msg.MsgId, make(chan *Msg, 20), s.sendBox, make(chan int), STATE_ACTIVE, bytes.Buffer{}, make(chan int)}

				s.connsMu.Lock()
				s.conns[msg.MsgId] = conn
				s.connsMu.Unlock()

				go conn.loop()
				// 发送成功消息
				s.sendBox <- &Msg{msg.MsgId, MSG_CONNECT_SUCCESS, 0, []byte{}}

				select {
				case s.accepts <- msg.MsgId:
				default:
					s.sendBox <- &Msg{msg.MsgId, MSG_CONNECT_ERROR, 0, []byte{}}
				}
			case MSG_CONNECT_ERROR:
				fallthrough
			case MSG_CONNECT_SUCCESS:
				fallthrough
			case MSG_CONN:
				// 内容
				s.connsMu.Lock()
				if conn, ok := s.conns[msg.MsgId]; ok {
					debugLog("loop recv", string(msg.Buff[:msg.Length]))
					conn.recvChan <- msg
				}
				s.connsMu.Unlock()

			case MSG_CLOSE:
				// 被动关闭
				s.connsMu.Lock()
				if conn, ok := s.conns[msg.MsgId]; ok {
					delete(s.conns, msg.MsgId)
					conn.closeChan <- 1
				}
				s.connsMu.Unlock()
			case MSG_KEEPALIVE:
				// ignore
			}
		}
	}()

	// 发送消息
	for {
		select {
		case msg := <-s.sendBox:
			err := s.sendMsg(msg)
			if err != nil {
				break
			}
			// 如果是关闭连接
			if msg.MsgType == MSG_CLOSE {
				s.connsMu.Lock()
				delete(s.conns, msg.MsgId)
				s.connsMu.Unlock()
			}
		case <-time.After(s.keepAliveTimeout):
			if s.mode == "client" {
				msg := &Msg{0, MSG_KEEPALIVE, 0, []byte{}}
				s.sendBox <- msg
			}
		}
	}
}

func (s *Smux) recvMsg() (*Msg, error) {
	header := make([]byte, 16)
	_, err := io.ReadFull(s.conn, header)
	if err != nil {
		return nil, err
	}

	msg := &Msg{}
	msg.MsgId = binary.BigEndian.Uint64(header)
	msg.MsgType = binary.BigEndian.Uint32(header[8:])
	msg.Length = binary.BigEndian.Uint32(header[12:])
	msg.Buff = make([]byte, msg.Length)

	if msg.Length > 0 {
		_, err = io.ReadFull(s.conn, msg.Buff)
		if err != nil {
			return nil, err
		}
	}
	return msg, nil
}

func (s *Smux) sendMsg(msg *Msg) error {
	buff := make([]byte, msg.Length+16)
	binary.BigEndian.PutUint64(buff, msg.MsgId)
	binary.BigEndian.PutUint32(buff[8:], msg.MsgType)
	binary.BigEndian.PutUint32(buff[12:], msg.Length)
	copy(buff[16:], msg.Buff)

	n, err := s.conn.Write(buff)
	if err != nil {
		return err
	}
	if n != len(buff) {
		return errors.New("content not match")
	}
	return nil
}
