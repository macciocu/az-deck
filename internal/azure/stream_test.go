package azure

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestExecURL(t *testing.T) {
	ct := Container{LogStream: "https://x.azurecontainerapps.dev/subscriptions/s/resourceGroups/rg/containerApps/a/revisions/r/replicas/p/containers/c/logstream"}
	got, err := ExecURL(ct, "/bin/bash")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://x.azurecontainerapps.dev/subscriptions/s/resourceGroups/rg/containerApps/a/revisions/r/replicas/p/containers/c/exec?command=%2Fbin%2Fbash"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	ct.Exec = "wss://y/exec"
	if got, _ := ExecURL(ct, "sh"); got != "wss://y/exec?command=sh" {
		t.Fatalf("explicit exec endpoint not used: %s", got)
	}
	if _, err := ExecURL(Container{}, "sh"); err == nil {
		t.Fatal("expected error without endpoints")
	}
}

func TestExecProtocol(t *testing.T) {
	received := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		up := websocket.Upgrader{}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_ = ws.WriteMessage(websocket.TextMessage, append([]byte{proxyInfo}, "connected"...))
		for i := 0; i < 2; i++ {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}
			received <- msg
		}
		_ = ws.WriteMessage(websocket.BinaryMessage, append([]byte{proxyForward, chanStdout}, "hello\n"...))
		_ = ws.WriteMessage(websocket.BinaryMessage, append([]byte{proxyForward, chanStderr}, "oops\n"...))
		_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer srv.Close()

	dialer := websocket.Dialer{}
	ws, _, err := dialer.DialContext(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), http.Header{"Authorization": {"Bearer tok"}})
	if err != nil {
		t.Fatal(err)
	}
	conn := &ExecConn{ws: ws}
	if _, err := conn.Write([]byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	if err := conn.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := conn.Pump(&out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := <-received; !bytes.Equal(got, []byte("\x00\x00ls\n")) {
		t.Fatalf("stdin frame = %q", got)
	}
	if got := <-received; !bytes.Equal(got, []byte("\x00\x04{\"Width\": 80, \"Height\": 24}")) {
		t.Fatalf("resize frame = %q", got)
	}
	if out.String() != "hello\n" {
		t.Fatalf("stdout = %q", out.String())
	}
	if !strings.Contains(errOut.String(), "INFO: connected") || !strings.Contains(errOut.String(), "oops") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestStreamLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tailLines") != "300" || q.Get("follow") != "true" || q.Get("output") != "text" {
			http.Error(w, "bad query "+r.URL.RawQuery, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("line one\n\nsay \\u0022hi\\u0022\n"))
	}))
	defer srv.Close()

	var lines []string
	err := streamLines(context.Background(), srv.URL+"/logstream", "tok", 1000, true, true, func(s string) { lines = append(lines, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "line one" || lines[1] != `say "hi"` {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRGFromID(t *testing.T) {
	id := "/subscriptions/s/resourceGroups/my-rg/providers/Microsoft.App/containerApps/app"
	if got := rgFromID(id); got != "my-rg" {
		t.Fatalf("rgFromID = %q", got)
	}
	if got := lastSegment(id); got != "app" {
		t.Fatalf("lastSegment = %q", got)
	}
}
