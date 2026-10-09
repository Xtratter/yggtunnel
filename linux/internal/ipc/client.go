package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
)

// Client is one connection to the daemon.
type Client struct {
	c       net.Conn
	mu      sync.Mutex
	next    int
	pending map[int]chan frame
	events  chan Event
	closed  chan struct{}
}

// Dial connects to the daemon socket.
func Dial(path string) (*Client, error) {
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	c := &Client{c: nc, pending: map[int]chan frame{}, events: make(chan Event, 256), closed: make(chan struct{})}
	go c.read()
	return c, nil
}

func (c *Client) read() {
	defer close(c.closed)
	sc := bufio.NewScanner(c.c)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for sc.Scan() {
		var f frame
		if json.Unmarshal(sc.Bytes(), &f) != nil {
			continue
		}
		if f.Event != "" {
			select {
			case c.events <- Event{Kind: f.Event, Data: f.Data}:
			default: // slow consumer: drop the event, never block the reader
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[f.ID]
		delete(c.pending, f.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- f
		}
	}
}

// Events delivers pushed events; closed never, drained by the caller.
func (c *Client) Events() <-chan Event { return c.events }

// Call sends a command and decodes the response data into out (when non-nil).
func (c *Client) Call(cmd string, args, out any) error {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return err
		}
		raw = b
	}
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan frame, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	b, _ := json.Marshal(Request{ID: id, Cmd: cmd, Args: raw})
	if _, err := c.c.Write(append(b, '\n')); err != nil {
		return err
	}
	select {
	case f := <-ch:
		if !f.OK {
			return errors.New(f.Error)
		}
		if out != nil && len(f.Data) > 0 {
			return json.Unmarshal(f.Data, out)
		}
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	}
}

// Close disconnects.
func (c *Client) Close() error { return c.c.Close() }
