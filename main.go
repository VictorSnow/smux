package main

import (
	"log"
	"time"
)

func main() {
	client := NewSmux("127.0.0.1:8099")
	server := NewSmux("127.0.0.1:8099")

	go server.startServer()

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

	err := client.startClient()
	log.Println(err)

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
