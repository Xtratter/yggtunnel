package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/ssh"
	"rsc.io/qr"
)

// Server setup over SSH, AmneziaVPN-style: the app logs in with the user's key,
// runs server.sh as root and reads the YGGTUNNEL_RESULT line back.

//go:embed server.sh
var serverScript string

//go:embed devices.sh
var devicesScript string

//go:embed wrapper.sh
var wrapperScript string

//go:embed status.sh
var statusScript string

//go:embed speed.sh
var speedScript string

//go:embed store.sh
var storeScript string

// SetupParams come from the app as JSON.
type SetupParams struct {
	Host       string            `json:"host"`
	Port       int               `json:"port"`
	User       string            `json:"user"`
	Key        string            `json:"key"`        // private key, OpenSSH/PEM text
	Passphrase string            `json:"passphrase"` // optional
	Password   string            `json:"password"`   // login by password (no key yet), and for sudo when the user is not root
	HostKey    string            `json:"hostKey"`    // known SHA256 fingerprint, "" on first connect
	ClientPub  string            `json:"clientPub"`  // phone's WireGuard public key
	WgPort     int               `json:"wgPort"`
	YggPort    int               `json:"yggPort"`
	Peers      []string          `json:"peers"`
	Mode       string            `json:"mode"`    // "" — full setup (server.sh), "devices" — devices.sh, "wrapper" — wrapper.sh, "status" — status.sh, "speed" — speed.sh, "store" — store.sh
	Env        map[string]string `json:"env"`     // extra variables for the script (ACTION, NAME_B64, CLIENT_NAME)
	WssPeer    string            `json:"wssPeer"` // the profile's wrapper address, for the server description
	// Result is what server.sh reported at setup (the app sends the whole profile): yggAddress is where the
	// server can also be reached, through the phone's own Yggdrasil node (dialSSH).
	Result struct {
		YggAddress string `json:"yggAddress"`
	} `json:"result"`
}

type setupState struct {
	Running bool            `json:"running"`
	Log     string          `json:"log"`
	HostKey string          `json:"hostKey,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	// SSHKey: after a full setup by password — the app's own private key, now in the server's
	// authorized_keys; the app keeps it and logs in with it from then on
	SSHKey string `json:"sshKey,omitempty"`
	Error  string `json:"error,omitempty"`
}

type setupJob struct {
	mu sync.Mutex
	st setupState
}

var setup setupJob

func (j *setupJob) logf(format string, a ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.st.Log += fmt.Sprintf(format, a...) + "\n"
}

func (j *setupJob) Status() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	b, _ := json.Marshal(j.st)
	return string(b)
}

// Start runs the setup in the background; the app polls Status.
func (j *setupJob) Start(paramsJSON string) error {
	var p SetupParams
	if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil {
		return err
	}
	j.mu.Lock()
	if j.st.Running {
		j.mu.Unlock()
		return errors.New("setup is already running")
	}
	j.st = setupState{Running: true}
	j.mu.Unlock()
	go func() {
		res, err := j.run(p)
		j.mu.Lock()
		defer j.mu.Unlock()
		j.st.Running = false
		if err != nil {
			j.st.Error = err.Error()
			j.st.Log += "error: " + err.Error() + "\n"
		} else {
			j.st.Result = res
		}
	}()
	return nil
}

func fingerprint(k ssh.PublicKey) string {
	h := sha256.Sum256(k.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(h[:])
}

func signer(p SetupParams) (ssh.Signer, error) {
	key := []byte(strings.TrimSpace(p.Key) + "\n")
	if p.Passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(key, []byte(p.Passphrase))
	}
	s, err := ssh.ParsePrivateKey(key)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		return nil, errors.New("the key is protected with a passphrase — enter it")
	}
	return s, err
}

// dial connects and checks the host key: trust on first use, then it must match.
func (j *setupJob) dial(p SetupParams) (*ssh.Client, error) {
	var auth []ssh.AuthMethod
	if strings.TrimSpace(p.Key) != "" {
		s, err := signer(p)
		if err != nil {
			return nil, fmt.Errorf("key: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(s))
	}
	if p.Password != "" {
		// plain password, and keyboard-interactive (how many servers ask for it) answering every prompt with it
		auth = append(auth, ssh.Password(p.Password), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			a := make([]string, len(qs))
			for i := range a {
				a[i] = p.Password
			}
			return a, nil
		}))
	}
	if len(auth) == 0 {
		return nil, errors.New("no SSH key or password")
	}
	cfg := &ssh.ClientConfig{
		User: p.User,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp := fingerprint(key)
			j.mu.Lock()
			j.st.HostKey = fp
			j.mu.Unlock()
			if p.HostKey != "" && p.HostKey != fp {
				return fmt.Errorf("server key changed: expected %s, got %s", p.HostKey, fp)
			}
			j.logf("Server key: %s", fp)
			return nil
		},
		Timeout: 20 * time.Second,
	}
	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	j.logf("Connecting to %s@%s", p.User, addr)
	c, err := j.dialSSH(addr, p, cfg)
	if err != nil && strings.Contains(err.Error(), "unable to authenticate") {
		if strings.TrimSpace(p.Key) == "" {
			return nil, errors.New("the server refused the password (wrong password, or password login is off in its sshd)")
		}
		return nil, errors.New("the server refused the key")
	}
	return c, err
}

// dialSSH dials the server's address; when that does not work and the profile knows the server's Yggdrasil
// address, it dials that one through the phone's running node (yggdial.go). The host key and the login are the same.
func (j *setupJob) dialSSH(addr string, p SetupParams, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	ygg, err := netip.ParseAddr(p.Result.YggAddress)
	if err != nil || !yggNet.Contains(ygg) {
		return ssh.Dial("tcp", addr, cfg)
	}
	direct := *cfg
	direct.Timeout = directDialTimeout
	c, err := ssh.Dial("tcp", addr, &direct)
	if err == nil || !shouldFallback(err) {
		return c, err
	}
	if !node.running() {
		j.logf("Through Yggdrasil: the VPN is off")
		return nil, fmt.Errorf("%w (turn the VPN on: the server can then be reached through Yggdrasil)", err)
	}
	j.logf("Connecting through Yggdrasil")
	via := net.JoinHostPort(ygg.String(), strconv.Itoa(p.Port))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, err2 := node.yggDial(ctx, via)
	if err2 != nil {
		j.logf("Through Yggdrasil: failed: %v", err2)
		return nil, fmt.Errorf("%w; through Yggdrasil: %v", err, err2)
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second)) // the handshake
	cc, chans, reqs, err2 := ssh.NewClientConn(conn, via, cfg)
	if err2 != nil {
		conn.Close()
		j.logf("Through Yggdrasil: failed: %v", err2)
		if !shouldFallback(err2) { // the server answered: its own error (login, key) is the one to show
			return nil, err2
		}
		return nil, fmt.Errorf("%w; through Yggdrasil: %v", err, err2)
	}
	_ = conn.SetDeadline(time.Time{})
	j.logf("Through Yggdrasil: connected")
	return ssh.NewClient(cc, chans, reqs), nil
}

// directDialTimeout: how long the server's own address gets when there is another way to it.
const directDialTimeout = 5 * time.Second

// shouldFallback: the server was not reached (or not spoken to); a refused login or a changed host key is an
// answer, not a reason to try another way.
func shouldFallback(err error) bool {
	m := err.Error()
	return !strings.Contains(m, "unable to authenticate") && !strings.Contains(m, "server key changed")
}

// installKey makes a new ed25519 key for the app and adds its public half to the login user's
// ~/.ssh/authorized_keys: after a setup by password the app logs in with the key. Returns the
// private key (OpenSSH PEM) once a login with it has been checked.
func (j *setupJob) installKey(c *ssh.Client, p SetupParams) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "yggtunnel")
	if err != nil {
		return "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " yggtunnel-app\n"
	sess, err := c.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(line)
	if out, err := sess.CombinedOutput(`umask 077; mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys`); err != nil {
		return "", fmt.Errorf("authorized_keys: %v %s", err, strings.TrimSpace(string(out)))
	}
	key := string(pem.EncodeToMemory(block))
	check := p
	check.Key, check.Passphrase, check.Password = key, "", ""
	check.HostKey = j.hostKey()
	cc, err := j.dial(check)
	if err != nil {
		return "", fmt.Errorf("logging in with the new key: %w", err)
	}
	cc.Close()
	j.logf("The app's own SSH key is added to ~/.ssh/authorized_keys; it logs in with it from now on")
	return key, nil
}

func (j *setupJob) hostKey() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st.HostKey
}

// QRMatrix encodes text as a QR code: JSON {size, rows: ["0101…", …]} for the app to draw.
func QRMatrix(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	rows := make([]string, c.Size)
	for y := range rows {
		b := make([]byte, c.Size)
		for x := range b {
			b[x] = '0'
			if c.Black(x, y) {
				b[x] = '1'
			}
		}
		rows[y] = string(b)
	}
	out, _ := json.Marshal(map[string]any{"size": c.Size, "rows": rows})
	return string(out), nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func validEnvName(k string) bool {
	if k == "" {
		return false
	}
	for _, c := range k {
		if !(c >= 'A' && c <= 'Z' || c == '_' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (j *setupJob) run(p SetupParams) (json.RawMessage, error) {
	c, err := j.dial(p)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	r, err := j.runOn(c, p)
	if err == nil && p.Mode == "" && strings.TrimSpace(p.Key) == "" && p.Password != "" {
		// set up by password: from now on by the app's own key
		if key, kerr := j.installKey(c, p); kerr != nil {
			j.logf("Could not add an SSH key (%v): the app keeps logging in by password", kerr)
		} else {
			j.mu.Lock()
			j.st.SSHKey = key
			j.mu.Unlock()
		}
	}
	switch p.Mode {
	case "", "devices", "wrapper", "speed":
		// after a change, the server's own plain description (/etc/yggtunnel/server.json) follows it
		if err == nil {
			m := p
			m.Mode, m.Env = "store", map[string]string{"ACTION": "manifest", "WSS_PEER": p.WssPeer}
			if _, merr := j.runOn(c, m); merr == nil {
				j.logf("Server description: /etc/yggtunnel/server.json")
			}
		}
	}
	return r, err
}

// runOn runs the mode's script in a new session of the connected client.
func (j *setupJob) runOn(c *ssh.Client, p SetupParams) (json.RawMessage, error) {
	sess, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	// Variables go in front of the script on stdin, not on the command line: the command line
	// is visible to everyone on the server (ps), and the device keys are secret.
	vars := map[string]string{
		"CLIENT_PUB": p.ClientPub, "WG_PORT": strconv.Itoa(p.WgPort), "YGG_PORT": strconv.Itoa(p.YggPort),
		"SSH_PORT": strconv.Itoa(p.Port), "YGG_PEERS": strings.Join(p.Peers, " "),
	}
	for k, v := range p.Env {
		if !validEnvName(k) {
			return nil, fmt.Errorf("bad variable name %q", k)
		}
		vars[k] = v
	}
	script := serverScript
	switch p.Mode {
	case "devices":
		script = devicesScript
	case "wrapper":
		script = wrapperScript
	case "status":
		script = statusScript
	case "speed":
		script = speedScript
	case "store":
		script = storeScript
	}
	var head strings.Builder
	for _, k := range sortedKeys(vars) {
		head.WriteString("export " + k + "=" + shellQuote(vars[k]) + "\n")
	}
	cmd := "bash -s"
	input := head.String() + script
	if p.User != "root" {
		cmd = "sudo -n " + cmd
		// sudo that wants a password: it reads one line from stdin (-S), the script follows it. Only after
		// a check that sudo really asks — else that line would reach bash as a command.
		if p.Password != "" && !j.sudoWithoutPassword(c) {
			cmd = "sudo -S -k -p '' bash -s"
			input = p.Password + "\n" + input
		}
	}
	// the scripts start with a shebang and comments; exports go first, the rest runs as is
	sess.Stdin = strings.NewReader(input)
	out, err := sess.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	sess.Stderr = &stderr
	if err := sess.Start(cmd); err != nil {
		return nil, err
	}
	var result json.RawMessage
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 8<<20) // a result line can be large (saved settings, the catalog)
	for sc.Scan() {
		line := sc.Text()
		if r, ok := strings.CutPrefix(line, "YGGTUNNEL_RESULT "); ok {
			result = json.RawMessage(r)
			continue
		}
		j.logf("%s", line)
	}
	err = sess.Wait()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		j.logf("%s", s)
	}
	if err != nil {
		if strings.Contains(stderr.String(), "sudo") {
			return nil, errors.New("root required: log in as root, or as a user with sudo (its password is used for sudo too)")
		}
		return nil, fmt.Errorf("the script failed: %w", err)
	}
	if result == nil || !json.Valid(result) {
		return nil, errors.New("no result from the server")
	}
	return result, nil
}

// sudoWithoutPassword: `sudo -n true` works for the login user.
func (j *setupJob) sudoWithoutPassword(c *ssh.Client) bool {
	s, err := c.NewSession()
	if err != nil {
		return false
	}
	defer s.Close()
	return s.Run("sudo -n true") == nil
}

// WgKeyPair returns a new WireGuard key pair as JSON {private, public} (base64).
func WgKeyPair() (string, error) {
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		return "", err
	}
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{
		"private": base64.StdEncoding.EncodeToString(priv[:]),
		"public":  base64.StdEncoding.EncodeToString(pub),
	})
	return string(b), nil
}
