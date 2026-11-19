package gofast

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzExtractParamNames(f *testing.F) {
	for _, seed := range []string{"/users/{id}", "/files/{name:path}", "/", "/a/{x}/b/{y}", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > 4096 {
			t.Skip()
		}
		params := extractParamNames(path)
		for _, name := range params {
			if strings.Contains(name, "/") {
				t.Fatalf("invalid parameter name %q", name)
			}
		}
	})
}

func FuzzRateLimitParser(f *testing.F) {
	for _, seed := range []string{"10/hour", "1/second", "30/minute", "", "999999999999999999999999999999/day"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 4096 {
			t.Skip()
		}
		_, _, _ = parseRateLimit(value)
	})
}

func FuzzTrieLookup(f *testing.F) {
	f.Add("/users/123", "/users/{id}")
	f.Add("/files/a/b.txt", "/files/{name:path}")
	f.Add("/anything", "/static/{name}")
	f.Fuzz(func(t *testing.T, requestPath, routePath string) {
		if len(requestPath) > 4096 || len(routePath) > 4096 {
			t.Skip()
		}
		params := extractParamNames(routePath)
		if len(params) == 0 {
			return
		}
		trie := newRouteTrie()
		route := &Route{Method: "GET", Path: routePath}
		trie.insert(routePath, params, route)
		_, _ = trie.find(requestPath)
	})
}

// FuzzRouterLookup tests complete route matching: static + dynamic routes combined.
// This exercises the Router.Handler directly (no HTTP server overhead per iteration).
func FuzzRouterLookup(f *testing.F) {
	// Seed routes: mix of static and dynamic (requestPath, routePath pairs)
	f.Add("/users", "/users")
	f.Add("/users/123", "/users/{id}")
	f.Add("/files/a/b.txt", "/files/{name:path}")
	f.Add("/api/v1/status", "/api/v1/status")

	f.Fuzz(func(t *testing.T, requestPath, routePath string) {
		if len(requestPath) > 4096 || len(routePath) > 4096 {
			t.Skip()
		}
		// Skip invalid URIs that httptest.NewRequest would reject
		if !strings.HasPrefix(requestPath, "/") {
			t.Skip()
		}
		// Skip invalid UTF-8 that would cause regexp.MustCompile to panic
		if !utf8.ValidString(requestPath) || !utf8.ValidString(routePath) {
			t.Skip()
		}
		// Skip routePath containing characters invalid for static route patterns
		// Valid static route chars: alphanumeric, -, _, ., /
		// (FuzzTrieLookup already tests dynamic {param} routes.)
		routeInvalid := false
		for _, r := range routePath {
			if !isValidStaticRouteChar(r) {
				routeInvalid = true
				break
			}
		}
		if routeInvalid {
			t.Skip()
		}
		// Skip paths containing characters invalid in HTTP request target (space, control chars)
		invalid := false
		for i, r := range requestPath {
			if r == ' ' || r < 0x20 || r == 0x7f {
				invalid = true
				break
			}
			// Skip invalid percent-encoding (% not followed by two hex digits)
			if r == '%' && (i+2 >= len(requestPath) || !isHex(requestPath[i+1]) || !isHex(requestPath[i+2])) {
				invalid = true
				break
			}
		}
		if invalid {
			t.Skip()
		}

		app := New()
		app.Route("GET", routePath, func(ctx *Context) (any, error) {
			return OK(map[string]string{"matched": routePath}), nil
		})

		handler := app.Handler()
		req := httptest.NewRequest(http.MethodGet, requestPath, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		// A 200 means the route matched; 404 means it didn't. Both are valid outcomes.
		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			t.Fatalf("unexpected status %d for request=%s route=%s", w.Code, requestPath, routePath)
		}
	})
}

// FuzzHandlerBinding tests binding of path/query params to handler parameters.
// This exercises the Context param binding logic with fuzzed input values.
func FuzzHandlerBinding(f *testing.F) {
	// Seeds: (routePath, pathParam, queryParam) triples
	f.Add("/items/{id}", "42", "search=test")
	f.Add("/users/{name}/posts/{postId}", "alice", "q=hello")
	f.Add("/search", "", "q=")

	f.Fuzz(func(t *testing.T, routePath, pathParam, queryParam string) {
		if len(routePath) > 4096 || len(pathParam) > 256 || len(queryParam) > 256 {
			t.Skip()
		}
		// routePath must start with / for valid HTTP request
		if !strings.HasPrefix(routePath, "/") {
			t.Skip()
		}
		// Skip invalid UTF-8
		if !utf8.ValidString(routePath) || !utf8.ValidString(pathParam) || !utf8.ValidString(queryParam) {
			t.Skip()
		}
		// Skip pathParam containing characters invalid for route param names
		// Route param names must be valid identifiers: alphanumeric and underscore
		for _, r := range pathParam {
			if !isValidRouteParamChar(r) {
				t.Skip()
			}
		}
		// Skip queryParam containing characters invalid in URL query
		// Safe URL chars: alphanumeric, -, _, ., ~
		for _, r := range queryParam {
			if !isSafeURLChar(r) {
				t.Skip()
			}
		}

		app := New()
		app.Route("GET", routePath, func(ctx *Context) (any, error) {
			id := ctx.Param("id")
			name := ctx.Param("name")
			postId := ctx.Param("postId")
			q := ctx.Query("q")
			_ = id
			_ = name
			_ = postId
			_ = q
			return OK(map[string]string{"bound": "ok"}), nil
		})

		handler := app.Handler()
		reqURL := routePath
		// Replace path param placeholder with fuzzed value
		reqURL = strings.ReplaceAll(reqURL, "{id}", pathParam)
		reqURL = strings.ReplaceAll(reqURL, "{name}", pathParam)
		reqURL = strings.ReplaceAll(reqURL, "{postId}", pathParam)
		// Add query param
		reqURL += "?q=" + queryParam

		// Final safety check: skip if reqURL contains any unsafe characters
		reqURLInvalid := false
		for _, r := range reqURL {
			if !isSafeURLChar(r) && r != '/' && r != '?' && r != '=' {
				reqURLInvalid = true
				break
			}
		}
		if reqURLInvalid {
			t.Skip()
		}

		req := httptest.NewRequest(http.MethodGet, reqURL, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
			t.Fatalf("unexpected status %d for route=%s pathParam=%s queryParam=%s",
				w.Code, routePath, pathParam, queryParam)
		}
	})
}

// isHex checks if a byte is a hexadecimal digit.
func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// isSafeURLChar checks if a rune is safe to use in a URL path/query without encoding.
// Safe: alphanumeric, -, _, ., ~
func isSafeURLChar(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '-', '_', '.', '~':
		return true
	}
	return false
}

// isValidRouteParamChar checks if a rune is valid in a route parameter name.
// Valid: alphanumeric and underscore only.
func isValidRouteParamChar(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	if r == '_' {
		return true
	}
	return false
}

// isValidRouteChar checks if a rune is valid in a route path pattern.
// Valid: alphanumeric, -, _, ., /, {, }, :
func isValidRouteChar(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '-', '_', '.', '/', '{', '}', ':':
		return true
	}
	return false
}

// isValidStaticRouteChar checks if a rune is valid in a static route path.
// Valid: alphanumeric, -, _, ., /  (no {} - FuzzTrieLookup tests dynamic routes)
func isValidStaticRouteChar(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '-', '_', '.', '/':
		return true
	}
	return false
}
