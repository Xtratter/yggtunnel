package core

// Default is the process-wide node used by the Android JNI layer and the Linux daemon.
func Default() *Node { return &node }

// LogString is the node log as one text.
func LogString() string { return logSink.String() }

// SetupStart starts the server setup job (see setup.go) from JSON parameters.
func SetupStart(params string) error { return setup.Start(params) }

// SetupStatus is the JSON status of the running or last setup job.
func SetupStatus() string { return setup.Status() }
