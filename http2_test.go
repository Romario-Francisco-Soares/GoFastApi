package gofast

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/net/http2"
)

func TestAppRun_RejectsHTTP2AndH2CTogether(t *testing.T) {
	app := New()
	cfg := DefaultServerConfig()
	cfg.HTTP2 = true
	cfg.H2C = true

	if err := app.Run(cfg); err == nil {
		t.Fatal("Run() should reject HTTP2 and H2C together")
	}
}

func TestAppRun_RejectsHTTP2WithoutTLS(t *testing.T) {
	app := New()
	cfg := DefaultServerConfig()
	cfg.HTTP2 = true

	if err := app.Run(cfg); err == nil {
		t.Fatal("Run() should reject HTTP2 without CertFile/KeyFile")
	}
}

func TestH2C_PriorKnowledge(t *testing.T) {
	app := New()
	app.GET("/ping", func(ctx *Context) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	srv := &http.Server{}
	handler, err := configureHTTP2(srv, app.Handler(), ServerConfig{H2C: true})
	if err != nil {
		t.Fatalf("configureHTTP2() error: %v", err)
	}

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}

	resp, err := client.Get(ts.URL + "/ping")
	if err != nil {
		t.Fatalf("h2c request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.ProtoMajor != 2 {
		t.Errorf("protocol = %s, want HTTP/2.0", resp.Proto)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
