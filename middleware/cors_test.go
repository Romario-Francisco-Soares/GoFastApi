package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSWildcardMethodsAndHeaders(t *testing.T) {
	middleware := CORS(CORSConfig{
		AllowOrigins:     []string{"https://client.example"},
		AllowMethods:     []string{"*"},
		AllowHeaders:     []string{"*"},
		AllowCredentials: true,
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "https://client.example")
	request.Header.Set("Access-Control-Request-Method", "PATCH")
	request.Header.Set("Access-Control-Request-Headers", "X-Custom, Content-Type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d; body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "PATCH" {
		t.Fatalf("allow methods=%q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "x-custom, content-type" {
		t.Fatalf("allow headers=%q", got)
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatal("credentials header missing")
	}
}

func TestCORSRejectsDisallowedOriginAndPreflightMethod(t *testing.T) {
	middleware := CORS(CORSConfig{AllowOrigins: []string{"https://allowed.example"}, AllowMethods: []string{"GET"}})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", "GET")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://allowed.example")
	request.Header.Set("Access-Control-Request-Method", "DELETE")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("disallowed method status=%d", response.Code)
	}
}
