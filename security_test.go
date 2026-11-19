package gofast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------- APIKey por header ----------

func TestAPIKey_Header_ValidSecret(t *testing.T) {
	app := New()
	app.Use(APIKey(APIKeyConfig{
		Name:   "X-API-Key",
		In:     APIKeyHeader,
		Secret: "super-secret-key",
	}))
	app.GET("/protected", func(ctx *Context) (any, error) {
		p, ok := ctx.Principal()
		if !ok {
			return nil, NewHTTPError(http.StatusInternalServerError, "no_principal", "principal missing")
		}
		return OK(map[string]string{"subject": p.Subject}), nil
	})

	handler := app.Handler()

	// Com chave válida
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "super-secret-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	// Sem chave
	req = httptest.NewRequest("GET", "/protected", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}

	// Com chave inválida
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// ---------- APIKey por query ----------

func TestAPIKey_Query_ValidSecret(t *testing.T) {
	app := New()
	app.Use(APIKey(APIKeyConfig{
		Name:   "api_key",
		In:     APIKeyQuery,
		Secret: "query-secret",
	}))
	app.GET("/protected", func(ctx *Context) (any, error) {
		return OK(map[string]string{"ok": "true"}), nil
	})

	handler := app.Handler()

	req := httptest.NewRequest("GET", "/protected?api_key=query-secret", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	req = httptest.NewRequest("GET", "/protected", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// ---------- APIKey por cookie ----------

func TestAPIKey_Cookie_ValidSecret(t *testing.T) {
	app := New()
	app.Use(APIKey(APIKeyConfig{
		Name:   "session_key",
		In:     APIKeyCookie,
		Secret: "cookie-secret",
	}))
	app.GET("/protected", func(ctx *Context) (any, error) {
		return OK(map[string]string{"ok": "true"}), nil
	})

	handler := app.Handler()

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "session_key", Value: "cookie-secret"})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	req = httptest.NewRequest("GET", "/protected", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// ---------- APIKey com validador customizado ----------

func TestAPIKey_CustomValidator(t *testing.T) {
	app := New()
	called := false
	app.Use(APIKey(APIKeyConfig{
		Name: "X-API-Key",
		In:   APIKeyHeader,
		Validator: func(r *http.Request, credential string) (*Principal, error) {
			called = true
			if credential == "validator-key" {
				return &Principal{Subject: "validator-user", Name: "Validator User"}, nil
			}
			return nil, nil
		},
	}))
	app.GET("/protected", func(ctx *Context) (any, error) {
		p, ok := ctx.Principal()
		if !ok {
			return nil, NewHTTPError(http.StatusInternalServerError, "no_principal", "principal missing")
		}
		return OK(map[string]string{"subject": p.Subject, "name": p.Name}), nil
	})

	handler := app.Handler()

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "validator-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if !called {
		t.Fatal("validador customizado não foi chamado")
	}

	// Chave inválida pelo validador
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("X-API-Key", "bad-key")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// ---------- OAuth2Bearer com validador customizado ----------

func TestOAuth2Bearer_CustomValidator(t *testing.T) {
	app := New()
	app.Use(OAuth2Bearer(func(r *http.Request, token string) (*Principal, error) {
		if token == "valid-oauth-token" {
			return &Principal{Subject: "oauth-user", Scopes: []string{"read", "write"}}, nil
		}
		return nil, nil
	}, "OAuth2"))

	app.GET("/protected", func(ctx *Context) (any, error) {
		p, ok := ctx.Principal()
		if !ok {
			return nil, NewHTTPError(http.StatusInternalServerError, "no_principal", "principal missing")
		}
		return OK(map[string]string{"subject": p.Subject, "scopes": strings.Join(p.Scopes, ",")}), nil
	})

	handler := app.Handler()

	// Com token válido
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer valid-oauth-token")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	// Sem token
	req = httptest.NewRequest("GET", "/protected", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}

	// Com tipo de autenticação errado (Basic em vez de Bearer)
	req = httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// ---------- HTTPBearer sem token ----------

func TestHTTPBearer_MissingToken(t *testing.T) {
	app := New()
	app.Use(HTTPBearer(BearerConfig{
		Secret: "test-token",
	}))
	app.GET("/protected", func(ctx *Context) (any, error) {
		return OK(map[string]string{"ok": "true"}), nil
	})

	handler := app.Handler()

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("WWW-Authenticate header não definido")
	}
}
