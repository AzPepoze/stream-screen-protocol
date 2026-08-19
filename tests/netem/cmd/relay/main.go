package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

const maxDatagramSize = 64 * 1024

type session struct {
	client   *net.UDPAddr
	upstream *net.UDPConn
	lastSeen time.Time
}

type relay struct {
	listener *net.UDPConn
	target   *net.UDPAddr
	idle     time.Duration

	mu       sync.Mutex
	sessions map[string]*session
}

func main() {
	listenAddr := env("LISTEN_ADDR", ":5000")
	targetAddr := os.Getenv("TARGET_ADDR")
	if targetAddr == "" {
		log.Fatal("TARGET_ADDR is required (example: host.docker.internal:7700)")
	}
	idleTimeout, err := time.ParseDuration(env("IDLE_TIMEOUT", "2m"))
	if err != nil {
		log.Fatalf("invalid IDLE_TIMEOUT: %v", err)
	}

	listenUDP, err := net.ResolveUDPAddr("udp", listenAddr)
	if err != nil {
		log.Fatalf("resolve LISTEN_ADDR: %v", err)
	}
	targetUDP, err := net.ResolveUDPAddr("udp", targetAddr)
	if err != nil {
		log.Fatalf("resolve TARGET_ADDR: %v", err)
	}
	listener, err := net.ListenUDP("udp", listenUDP)
	if err != nil {
		log.Fatalf("listen %s: %v", listenAddr, err)
	}
	defer listener.Close()

	r := &relay{
		listener: listener,
		target:   targetUDP,
		idle:     idleTimeout,
		sessions: make(map[string]*session),
	}

	log.Printf("UDP impairment relay listening=%s target=%s idle_timeout=%s", listener.LocalAddr(), targetUDP, idleTimeout)
	go r.reapIdle()
	if err := r.run(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Fatal(err)
	}
}

func (r *relay) run() error {
	buf := make([]byte, maxDatagramSize)
	for {
		n, client, err := r.listener.ReadFromUDP(buf)
		if err != nil {
			return fmt.Errorf("read client datagram: %w", err)
		}
		payload := append([]byte(nil), buf[:n]...)
		s, err := r.sessionFor(client)
		if err != nil {
			log.Printf("create session client=%s: %v", client, err)
			continue
		}
		r.touch(client.String())
		if _, err := s.upstream.Write(payload); err != nil {
			log.Printf("forward client=%s target=%s: %v", client, r.target, err)
			r.dropSession(client.String(), s)
		}
	}
}

func (r *relay) sessionFor(client *net.UDPAddr) (*session, error) {
	key := client.String()
	r.mu.Lock()
	if s := r.sessions[key]; s != nil {
		r.mu.Unlock()
		return s, nil
	}

	upstream, err := net.DialUDP("udp", nil, r.target)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	copyClient := *client
	s := &session{client: &copyClient, upstream: upstream, lastSeen: time.Now()}
	r.sessions[key] = s
	r.mu.Unlock()

	log.Printf("session opened client=%s upstream_local=%s target=%s", client, upstream.LocalAddr(), r.target)
	go r.copyReplies(key, s)
	return s, nil
}

func (r *relay) copyReplies(key string, s *session) {
	buf := make([]byte, maxDatagramSize)
	for {
		n, err := s.upstream.Read(buf)
		if err != nil {
			r.dropSession(key, s)
			return
		}
		r.touch(key)
		if _, err := r.listener.WriteToUDP(buf[:n], s.client); err != nil {
			log.Printf("reply target=%s client=%s: %v", r.target, s.client, err)
			r.dropSession(key, s)
			return
		}
	}
}

func (r *relay) touch(key string) {
	r.mu.Lock()
	if s := r.sessions[key]; s != nil {
		s.lastSeen = time.Now()
	}
	r.mu.Unlock()
}

func (r *relay) reapIdle() {
	interval := r.idle / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for now := range ticker.C {
		r.mu.Lock()
		for key, s := range r.sessions {
			if now.Sub(s.lastSeen) >= r.idle {
				delete(r.sessions, key)
				_ = s.upstream.Close()
				log.Printf("session expired client=%s", s.client)
			}
		}
		r.mu.Unlock()
	}
}

func (r *relay) dropSession(key string, expected *session) {
	r.mu.Lock()
	if current := r.sessions[key]; current == expected {
		delete(r.sessions, key)
		_ = current.upstream.Close()
	}
	r.mu.Unlock()
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
