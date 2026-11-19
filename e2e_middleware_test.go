package gofast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- Middleware Recovery ----------

func TestApp_Use_Recovery(t *testing.T) {
	app := New()
	app.Use(Recovery(nil))

	app.GET("/panic", func(ctx *Context) (any, error) {
		panic("algo deu errado")
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status esperado 500, obtido %d", w.Code)
	}
}

// ---------- Middleware RequestID ----------

func TestApp_Use_RequestID(t *testing.T) {
	app := New()
	app.Use(RequestID(""))

	app.GET("/id", func(ctx *Context) (any, error) {
		return map[string]string{"ok": "true"}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/id", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
	requestID := w.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Error("header X-Request-ID não definido na resposta")
	}
}

// ---------- Middleware CORS ----------

func TestApp_Use_CORS(t *testing.T) {
	app := New()
	app.Use(CORS(CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: false,
	}))

	app.GET("/cors", func(ctx *Context) (any, error) {
		return map[string]string{"ok": "true"}, nil
	})

	handler := app.Handler()

	// Teste preflight OPTIONS
	req := httptest.NewRequest("OPTIONS", "/cors", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status esperado 204 para preflight CORS, obtido %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("Access-Control-Allow-Origin esperado '*', obtido %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

// ---------- Middleware Compression ----------

func TestApp_Use_Compression(t *testing.T) {
	app := New()
	app.Use(Compression())

	app.GET("/compress", func(ctx *Context) (any, error) {
		return strings.Repeat("a", 1000), nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/compress", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding esperado 'gzip', obtido %q", w.Header().Get("Content-Encoding"))
	}
}

// ---------- Middleware Timeout ----------

func TestApp_Use_Timeout(t *testing.T) {
	app := New()
	app.Use(Timeout(10 * time.Millisecond))

	app.GET("/slow", func(ctx *Context) (any, error) {
		time.Sleep(100 * time.Millisecond)
		return map[string]string{"ok": "true"}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/slow", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status esperado 503 para timeout, obtido %d", w.Code)
	}
}
