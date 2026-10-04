package main

import (
	"strings"
	"sync"
	"time"
)

// ringLog keeps the last lines of the node log so the app can show them
// without adb/logcat.
type ringLog struct {
	mu    sync.Mutex
	lines []string
}

const logLines = 300

var logSink = &ringLog{}

func (r *ringLog) Write(p []byte) (int, error) {
	r.add(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func (r *ringLog) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, time.Now().Format("15:04:05 ")+s)
	if len(r.lines) > logLines {
		r.lines = r.lines[len(r.lines)-logLines:]
	}
}

func (r *ringLog) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}
