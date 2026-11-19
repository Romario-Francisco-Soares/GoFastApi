package gofast

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// ---------- Setup helpers ----------

func benchApp() *App {
	return New()
}

func benchHandler(ctx *Context) (any, error) {
	return OK(map[string]string{"status": "ok"}), nil
}

// ---------- Benchmark: Handler HTTP direto (net/http puro) ----------

func BenchmarkHandlerDirect(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Read(make([]byte, 1024))
		resp.Body.Close()
	}
}

// ---------- Benchmark: Rota estática GoFastAPI ----------

func BenchmarkRouterStatic(b *testing.B) {
	app := New()
	app.GET("/static", benchHandler)

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/static", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Read(make([]byte, 1024))
		resp.Body.Close()
	}
}

// ---------- Benchmark: Rota dinâmica GoFastAPI (trie) ----------

func BenchmarkRouterDynamic(b *testing.B) {
	app := New()
	app.GET("/usuarios/{id}", benchHandler)

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/usuarios/12345", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Read(make([]byte, 1024))
		resp.Body.Close()
	}
}

// ---------- Benchmark: JSON response ----------

func BenchmarkJSONResponse(b *testing.B) {
	data := map[string]any{
		"status":  "ok",
		"message": "operacao realizada com sucesso",
		"data": map[string]any{
			"id":      12345,
			"nome":    "usuario-teste",
			"ativo":   true,
			"email":   "usuario@exemplo.com",
			"tags":    []string{"a", "b", "c"},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp := NewJSONResponse(data, StatusOK)
		_ = resp
	}
}

// ---------- Benchmark: Middleware chain ----------

func BenchmarkMiddlewareChain(b *testing.B) {
	app := New()
	app.Use(Recovery(nil))
	app.Use(RequestID(""))
	app.Use(CORS(CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
	}))
	app.Use(Compression())
	app.Use(Timeout(10 * time.Second))
	app.GET("/chain", benchHandler)

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/chain", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Read(make([]byte, 1024))
		resp.Body.Close()
	}
}

// ---------- Benchmark: DI Singleton ----------

func BenchmarkDI_Singleton(b *testing.B) {
	d := NewDIContainer()
	d.Provide(func() *benchService { return &benchService{} }, Singleton())
	t := reflect.TypeOf((*benchService)(nil))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = d.Resolve(t, nil)
	}
}

// ---------- Benchmark: DI Request scope ----------

func BenchmarkDI_Request(b *testing.B) {
	d := NewDIContainer()
	d.Provide(func() *benchService { return &benchService{} }, Request())
	t := reflect.TypeOf((*benchService)(nil))

	ctx := NewContext(nil, &http.Request{}, nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = d.Resolve(t, ctx)
	}
}

// ---------- Benchmark: DI Transient ----------

func BenchmarkDI_Transient(b *testing.B) {
	d := NewDIContainer()
	d.Provide(func() *benchService { return &benchService{} }, Transient())
	t := reflect.TypeOf((*benchService)(nil))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = d.Resolve(t, nil)
	}
}

// ---------- Benchmark: Router group ----------

func BenchmarkRouterGroup(b *testing.B) {
	app := New()
	grupo := app.Group("/api/v1", GroupOption{Tags: []string{"api"}})
	grupo.GET("/usuarios/{id}", benchHandler)

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/usuarios/12345", nil)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Read(make([]byte, 1024))
		resp.Body.Close()
	}
}

// ---------- Test helper ----------

type benchService struct {
	ID   int
	Nome string
}
