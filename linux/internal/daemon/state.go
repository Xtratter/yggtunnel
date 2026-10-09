package daemon

import (
	"encoding/json"

	"github.com/Xtratter/yggtunnel/linux/internal/ipc"
	"github.com/Xtratter/yggtunnel/linux/internal/profile"
	"github.com/Xtratter/yggtunnel/linux/internal/store"
)

// State is the connection state. Names match the Android app's phases.
type State string

const (
	Off          State = "off"
	Starting     State = "starting"
	Connected    State = "connected"
	Reconnecting State = "reconnecting"
	Error        State = "error"
)

// Status is the answer to the "status" command.
type Status struct {
	State   State           `json:"state"`
	Error   string          `json:"error,omitempty"` // why the last attempt failed
	Node    json.RawMessage `json:"node,omitempty"`  // core status, while running
	Profile profile.Profile `json:"profile"`         // active profile, private key masked

	Settings         store.Settings `json:"settings"`
	KillSwitchActive bool           `json:"killSwitchActive"` // armed right now (only while connected)
}

func (d *Daemon) setState(s State, errText string) {
	d.mu.Lock()
	d.state = s
	if s == Starting {
		d.lastErr = ""
	}
	if errText != "" {
		d.lastErr = errText
	}
	d.mu.Unlock()
	data, _ := json.Marshal(struct {
		State State  `json:"state"`
		Error string `json:"error,omitempty"`
	}{s, errText})
	d.emit(ipc.Event{Kind: "state", Data: data})
}
