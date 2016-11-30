package smux

import (
	"log"
	"testing"
	"time"
)

func Test_main(t testing.T) {
	client := NewSmux("127.0.0.1:8099", "client")
	server := NewSmux("127.0.0.1:8099", "server")

	go server.start()

	go func() {
		for {
			buff := make([]byte, 2048)
			conn := server.Accept()
			n, err := conn.Recv(buff)
			log.Println("server loop", string(buff[:n]), err)
			conn.Send(buff[:n])
		}
	}()

	time.Sleep(time.Second)

	client.start()

	c, err := client.Dail()
	log.Println(c, err)

	c.Send([]byte("hello world"))
	buff2 := make([]byte, 2048)
	n, err := c.Recv(buff2)
	log.Println("client loop", string(buff2[:n]), err)

	c.Close()

	for {
		time.Sleep(time.Second * 5)
	}
}
