package gofast

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---------- App.Route: método arbitrário ----------

func TestApp_Route_ArbitraryMethod(t *testing.T) {
	app := New()

	app.Route("PROPFIND", "/arquivos/{id}", func(ctx *Context) (any, error) {
		return map[string]string{"method": ctx.Method(), "id": ctx.Param("id")}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("PROPFIND", "/arquivos/123", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}

// ---------- App.Group: prefixo ----------

func TestApp_Group_Prefix(t *testing.T) {
	app := New()

	usuarios := app.Group("/usuarios")
	usuarios.GET("/{id}", func(ctx *Context) (any, error) {
		return map[string]string{"id": ctx.Param("id")}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/usuarios/42", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}

// ---------- App.Group: prefixo aninhado ----------

func TestApp_Group_NestedPrefix(t *testing.T) {
	app := New()

	v1 := app.Group("/api/v1")
	v1.GET("/status", func(ctx *Context) (any, error) {
		return map[string]string{"version": "v1"}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/api/v1/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}

// ---------- App.Group: tags herdadas ----------

func TestApp_Group_InheritedTags(t *testing.T) {
	app := New()

	usuarios := app.Group("/usuarios", GroupOption{Tags: []string{"usuarios"}})
	usuarios.GET("/{id}", func(ctx *Context) (any, error) {
		return map[string]string{"id": ctx.Param("id")}, nil
	})

	doc := app.OpenAPI()
	pathVal, ok := doc.Paths["/usuarios/{id}"]
	if !ok {
		t.Fatalf("path /usuarios/{id} não encontrado no OpenAPI. Paths: %v", doc.Paths)
	}
	getOp, ok := pathVal.(map[string]any)["get"].(map[string]any)
	if !ok {
		t.Fatal("operação GET não encontrada")
	}
	tags, ok := getOp["tags"].([]string)
	if !ok || len(tags) == 0 {
		t.Errorf("tags esperadas na rota do grupo, obtido: %v (%T)", getOp["tags"], getOp["tags"])
	}
	if tags[0] != "usuarios" {
		t.Errorf("tag esperada 'usuarios', obtido: %v", tags)
	}
}

// ---------- Router.Group independente ----------

func TestRouter_Group_Independent(t *testing.T) {
	r := NewRouter()

	api := r.Group("/api", GroupOption{Tags: []string{"api"}})
	api.GET("/ping", func(ctx *Context) (any, error) {
		return map[string]string{"status": "ok"}, nil
	})

	handler := r.Handler(nil)
	req := httptest.NewRequest("GET", "/api/ping", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}

// ---------- App.Group: dependências do grupo ----------

func TestApp_Group_InheritedDependencies(t *testing.T) {
	app := New()

	usuarios := app.Group("/usuarios")
	usuarios.GET("/me", func(ctx *Context) (any, error) {
		return map[string]string{"ok": "true"}, nil
	})

	handler := app.Handler()
	req := httptest.NewRequest("GET", "/usuarios/me", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}
