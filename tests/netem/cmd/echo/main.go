package main

import (
	"log"
	"net"
	"os"
)

func main() {
	listenAddr := ":7700"
	if value := os.Getenv("LISTEN_ADDR"); value != "" {
		listenAddr = value
	}
	addr, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("UDP smoke echo listening=%s", conn.LocalAddr())

	buf := make([]byte, 64*1024)
	for {
		n, peer, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := conn.WriteToUDP(buf[:n], peer); err != nil {
			log.Printf("echo to %s: %v", peer, err)
		}
	}
}
