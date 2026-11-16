package gofast

import (
	"strings"
	"testing"
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
