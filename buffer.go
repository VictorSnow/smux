package smux

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
)

type WaitableBuff struct {
	buf    bytes.Buffer
	cond   *sync.Cond
	active int32
	mu     sync.Mutex
}

func NewWaitableBuff() *WaitableBuff {
	locker := &sync.Mutex{}
	return &WaitableBuff{
		buf:    bytes.Buffer{},
		cond:   sync.NewCond(locker),
		active: 1,
		mu:     sync.Mutex{},
	}
}

func (b *WaitableBuff) Read(buff []byte) (n int, err error) {
	// if closed
	if atomic.LoadInt32(&b.active) == 0 {
		return 0, io.EOF
	}

	b.mu.Lock()
	n, err = b.buf.Read(buff)
	b.mu.Unlock()

	if n > 0 {
		return
	}

	// if no buffer to read
	if n == 0 && err == io.EOF {
		b.wait()
		return b.Read(buff)
	}
	return
}

func (b *WaitableBuff) Write(buff []byte) (n int, err error) {
	// if closed
	if atomic.LoadInt32(&b.active) == 0 {
		return 0, io.EOF
	}

	b.mu.Lock()
	n, err = b.buf.Write(buff)
	b.mu.Unlock()

	// notify to read
	if n > 0 {
		b.notify()
	}
	return
}

func (b *WaitableBuff) wait() {
	b.cond.L.Lock()
	b.cond.Wait()
	b.cond.L.Unlock()
}

func (b *WaitableBuff) notify() {
	b.cond.L.Lock()
	b.cond.Signal()
	b.cond.L.Unlock()
}

func (b *WaitableBuff) Close() {
	atomic.StoreInt32(&b.active, 0)
	b.notify()
}
