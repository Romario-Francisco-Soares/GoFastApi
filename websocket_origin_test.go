package gofast

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newWebSocketTestServer(t *testing.T, opts ...any) *httptest.Server {
	t.Helper()
	app := New()
	app.WebSocket("/ws", func(conn *WebSocketConn) error { return nil }, opts...)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func doWebSocketUpgrade(t *testing.T, serverURL, origin string) *http.Response {
	t.Helper()

	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var request strings.Builder
	fmt.Fprintf(&request,
		"GET /ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n",
		u.Host, key)
	if origin != "" {
		fmt.Fprintf(&request, "Origin: %s\r\n", origin)
	}
	request.WriteString("\r\n")

	if _, err := conn.Write([]byte(request.String())); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}
	return resp
}

func TestWebSocketSameOriginAccepted(t *testing.T) {
	srv := newWebSocketTestServer(t)
	u, _ := url.Parse(srv.URL)

	resp := doWebSocketUpgrade(t, srv.URL, "http://"+u.Host)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
}

func TestWebSocketCrossOriginRejected(t *testing.T) {
	srv := newWebSocketTestServer(t)

	resp := doWebSocketUpgrade(t, srv.URL, "https://evil.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestWebSocketWithoutOriginAccepted(t *testing.T) {
	srv := newWebSocketTestServer(t)

	resp := doWebSocketUpgrade(t, srv.URL, "")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
}

func TestWebSocketAllowAllOriginsOption(t *testing.T) {
	srv := newWebSocketTestServer(t, WebSocketOptions{CheckOrigin: AllowAllWebSocketOrigins})

	resp := doWebSocketUpgrade(t, srv.URL, "https://evil.example")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", resp.StatusCode)
	}
}

func TestWebSocketCustomOriginChecker(t *testing.T) {
	srv := newWebSocketTestServer(t, WebSocketOptions{
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == "https://trusted.example"
		},
	})

	if got := doWebSocketUpgrade(t, srv.URL, "https://trusted.example").StatusCode; got != http.StatusSwitchingProtocols {
		t.Fatalf("trusted origin status = %d, want 101", got)
	}
	if got := doWebSocketUpgrade(t, srv.URL, "https://other.example").StatusCode; got != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %d, want 403", got)
	}
}

func TestSameOriginWebSocketCheck(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://api.example/ws", nil)
	req.Host = "api.example"

	if !SameOriginWebSocketCheck(req) {
		t.Error("request without Origin should be accepted")
	}

	req.Header.Set("Origin", "http://api.example")
	if !SameOriginWebSocketCheck(req) {
		t.Error("same-origin request should be accepted")
	}

	req.Header.Set("Origin", "http://evil.example")
	if SameOriginWebSocketCheck(req) {
		t.Error("cross-origin request should be rejected")
	}

	req.Header.Set("Origin", "not a url\x7f")
	if SameOriginWebSocketCheck(req) {
		t.Error("malformed Origin should be rejected")
	}
}
