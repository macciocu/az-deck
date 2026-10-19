package azure

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// MaxTail is the largest tailLines value the Container Apps log API accepts.
const MaxTail = 300

// eventStreamReplacer undoes the escaping the event stream API applies.
var eventStreamReplacer = strings.NewReplacer(`\u0022`, `"`, `\u001B`, "\x1b", `\u002B`, "+", `\u0027`, "'")

// StreamAppLogs streams console logs of a container (logStream URL) using an
// app-scoped token. emit is called for every line until ctx is cancelled or
// the stream ends.
func (c *Client) StreamAppLogs(ctx context.Context, rg, app, endpoint string, tail int, follow bool, emit func(string)) error {
	token, err := c.AuthToken(ctx, rg, app)
	if err != nil {
		return err
	}
	return streamLines(ctx, endpoint, token, tail, follow, true, emit)
}

// StreamEnvLogs streams the system event log of a managed environment.
func (c *Client) StreamEnvLogs(ctx context.Context, rg, env, endpoint string, tail int, follow bool, emit func(string)) error {
	token, err := c.EnvAuthToken(ctx, rg, env)
	if err != nil {
		return err
	}
	return streamLines(ctx, endpoint, token, tail, follow, false, emit)
}

func streamLines(ctx context.Context, endpoint, token string, tail int, follow, text bool, emit func(string)) error {
	if endpoint == "" {
		return errors.New("no log stream endpoint available")
	}
	if tail < 0 || tail > MaxTail {
		tail = MaxTail
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("follow", strconv.FormatBool(follow))
	q.Set("tailLines", strconv.Itoa(tail))
	if text {
		q.Set("output", "text")
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("log stream: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		emit(eventStreamReplacer.Replace(line))
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// Exec protocol bytes, as used by `az containerapp exec`.
const (
	proxyForward = 0
	proxyInfo    = 1
	proxyError   = 2

	chanStdin  = 0
	chanStdout = 1
	chanStderr = 2
	chanResize = 4
)

// ExecConn is an interactive shell session in a container.
type ExecConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

// ExecURL derives the exec websocket URL for a container.
func ExecURL(ct Container, command string) (string, error) {
	base := ct.Exec
	if base == "" {
		if ct.LogStream == "" {
			return "", errors.New("container exposes no exec endpoint")
		}
		base = strings.TrimSuffix(ct.LogStream, "/logstream") + "/exec"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if u.Scheme == "https" || u.Scheme == "http" {
		u.Scheme = "wss"
	}
	q := u.Query()
	q.Set("command", command)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exec opens an interactive session running command in the container.
func (c *Client) Exec(ctx context.Context, ct Container, command string) (*ExecConn, error) {
	token, err := c.AuthToken(ctx, ct.RG, ct.App)
	if err != nil {
		return nil, err
	}
	target, err := ExecURL(ct, command)
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second, Proxy: http.ProxyFromEnvironment}
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	ws, resp, err := dialer.DialContext(ctx, target, hdr)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("exec: %s", resp.Status)
		}
		return nil, err
	}
	return &ExecConn{ws: ws}, nil
}

func (e *ExecConn) send(prefix []byte, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ws.WriteMessage(websocket.TextMessage, append(prefix, data...))
}

// Write sends stdin bytes to the remote shell.
func (e *ExecConn) Write(p []byte) (int, error) {
	if err := e.send([]byte{proxyForward, chanStdin}, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Resize informs the remote terminal of a new window size.
func (e *ExecConn) Resize(width, height int) error {
	return e.send([]byte{proxyForward, chanResize}, fmt.Appendf(nil, `{"Width": %d, "Height": %d}`, width, height))
}

// Pump copies remote output to stdout/stderr until the session ends. Proxy
// info/error messages are written to stderr.
func (e *ExecConn) Pump(stdout, stderr io.Writer) error {
	for {
		_, msg, err := e.ws.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case proxyInfo:
			fmt.Fprintf(stderr, "INFO: %s\r\n", msg[1:])
		case proxyError:
			fmt.Fprintf(stderr, "ERROR: %s\r\n", msg[1:])
		case proxyForward:
			if len(msg) < 2 {
				continue
			}
			switch msg[1] {
			case chanStdout:
				_, _ = stdout.Write(msg[2:])
			case chanStderr:
				_, _ = stderr.Write(msg[2:])
			}
		}
	}
}

// Close terminates the session.
func (e *ExecConn) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	return e.ws.Close()
}
