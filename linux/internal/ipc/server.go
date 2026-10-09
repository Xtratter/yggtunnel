package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

// Server accepts clients on a unix socket.
type Server struct {
	l       net.Listener
	h       Handler
	mu      sync.Mutex
	clients map[*conn]struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

type conn struct {
	c    net.Conn
	out  chan []byte
	once sync.Once
}

func (c *conn) close() { c.once.Do(func() { c.c.Close() }) }

// send queues a line; a client that cannot keep up is dropped rather than blocking the daemon.
func (c *conn) send(b []byte) {
	select {
	case c.out <- b:
	default:
		c.close()
	}
}

// Listen creates the socket with mode 0660 owned by group.
func Listen(path, group string, h Handler) (*Server, error) {
	g, err := user.LookupGroup(group)
	if err != nil {
		return nil, fmt.Errorf("group %q: %w", group, err)
	}
	gid, _ := strconv.Atoi(g.Gid)
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(path, -1, gid); err != nil {
		l.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{l: l, h: h, clients: map[*conn]struct{}{}, ctx: ctx, cancel: cancel}
	go s.accept()
	return s, nil
}

func (s *Server) accept() {
	for {
		nc, err := s.l.Accept()
		if err != nil {
			return
		}
		c := &conn{c: nc, out: make(chan []byte, 256)}
		s.mu.Lock()
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		go s.writer(c)
		go s.serve(c)
	}
}

func (s *Server) writer(c *conn) {
	for b := range c.out {
		if _, err := c.c.Write(b); err != nil {
			c.close()
			return
		}
	}
}

func peerOf(c net.Conn) Peer {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return Peer{UID: -1, GID: -1, PID: -1}
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{UID: -1, GID: -1, PID: -1}
	}
	p := Peer{UID: -1, GID: -1, PID: -1}
	_ = raw.Control(func(fd uintptr) {
		if cr, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			p = Peer{UID: int(cr.Uid), GID: int(cr.Gid), PID: int(cr.Pid)}
		}
	})
	return p
}

func (s *Server) serve(c *conn) {
	defer func() {
		c.close()
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
		close(c.out)
	}()
	peer := peerOf(c.c)
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		var req Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil || req.Cmd == "" {
			return
		}
		resp := Response{ID: req.ID, OK: true}
		data, err := s.h.Handle(s.ctx, peer, req)
		if err != nil {
			resp.OK, resp.Error = false, err.Error()
		} else if data != nil {
			if resp.Data, err = json.Marshal(data); err != nil {
				resp.OK, resp.Error = false, err.Error()
			}
		}
		b, _ := json.Marshal(resp)
		c.send(append(b, '\n'))
	}
}

// Broadcast pushes an event to every connected client.
func (s *Server) Broadcast(ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.send(b)
	}
}

// Close stops accepting and disconnects all clients.
func (s *Server) Close() error {
	s.cancel()
	err := s.l.Close()
	s.mu.Lock()
	for c := range s.clients {
		c.close()
	}
	s.mu.Unlock()
	return err
}
