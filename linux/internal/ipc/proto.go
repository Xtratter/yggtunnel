// Package ipc is the line-delimited JSON protocol between the daemon and its clients
// (the window, yggtunnelctl) over a unix socket.
package ipc

import (
	"context"
	"encoding/json"
)

// Request is one command from a client.
type Request struct {
	ID   int             `json:"id"`
	Cmd  string          `json:"cmd"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response answers the request with the same ID.
type Response struct {
	ID    int             `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Event is pushed to every connected client. Kind: "state", "log", "peers".
type Event struct {
	Kind string          `json:"event"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Peer identifies the process on the other end of the socket (SO_PEERCRED).
type Peer struct{ UID, GID, PID int }

// Handler executes a command; the returned value is JSON-encoded into Response.Data.
type Handler interface {
	Handle(ctx context.Context, peer Peer, req Request) (any, error)
}

const maxLine = 1 << 20

// frame is what the client reads: a response or an event.
type frame struct {
	ID    int             `json:"id"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
	Event string          `json:"event,omitempty"`
}
