package gofast

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

var benchmarkPayload = map[string]any{"id": 1, "status": "ok", "items": []string{"alpha", "beta", "gamma"}}

func BenchmarkGoHTTPDirect(b *testing.B) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(benchmarkPayload)
	})
	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkGoFastAPIStaticRoute(b *testing.B) {
	app := New()
	app.GET("/bench", func(ctx *Context) (any, error) { return benchmarkPayload, nil })
	handler := app.Handler()
	req := httptest.NewRequest(http.MethodGet, "/bench", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkGoFastAPIDynamicRoute(b *testing.B) {
	app := New()
	app.GET("/items/{id}", func(ctx *Context) (any, error) { return map[string]string{"id": ctx.Param("id")}, nil })
	handler := app.Handler()
	req := httptest.NewRequest(http.MethodGet, "/items/42", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
}

func BenchmarkGoFastAPIJSONResponse(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewJSONResponse(benchmarkPayload, http.StatusOK)
	}
}
