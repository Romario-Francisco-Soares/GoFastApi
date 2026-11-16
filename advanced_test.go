package gofast

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBackgroundTaskRunsAfterResponse(t *testing.T) {
	app := New()
	completed := make(chan struct{}, 1)
	app.GET("/task", func(ctx *Context) (any, error) {
		ctx.BackgroundTasks().Add(func() { completed <- struct{}{} })
		return OK(map[string]string{"ok": "yes"}), nil
	})
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/task")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("background task did not execute")
	}
}

func TestEncryptedCookieSessionPersistsBetweenRequests(t *testing.T) {
	app := New()
	app.Use(SessionMiddleware(SessionConfig{Secret: "this-is-a-long-test-secret-123", CookieName: "sid", HTTPOnly: true, Secure: false, MaxAge: time.Hour}))
	app.GET("/login", func(ctx *Context) (any, error) {
		ctx.Session().Set("user", "alice")
		return OK(map[string]string{"status": "ok"}), nil
	})
	app.GET("/profile", func(ctx *Context) (any, error) {
		return OK(map[string]string{"user": ctx.Session().GetString("user", "")}), nil
	})
	client := NewTestClient(app)
	defer client.Close()
	login, err := client.Get("/login")
	if err != nil {
		t.Fatal(err)
	}
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", login.StatusCode)
	}
	login.Body.Close()
	profile, err := client.Get("/profile")
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, profile)
	if got["user"] != "alice" {
		t.Fatalf("session not restored: %#v", got)
	}
}

func TestSessionRejectsShortSecret(t *testing.T) {
	app := New()
	app.Use(SessionMiddleware(SessionConfig{Secret: "short"}))
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHTTPBearerAndPrincipal(t *testing.T) {
	app := New()
	app.Use(HTTPBearer(BearerConfig{Secret: "test-token"}))
	app.GET("/me", func(ctx *Context) (any, error) {
		p, ok := ctx.Principal()
		if !ok {
			return nil, NewHTTPError(401, "unauthorized", "missing principal")
		}
		return OK(map[string]string{"subject": p.Subject}), nil
	})
	client := NewTestClient(app)
	defer client.Close()
	req, _ := client.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, resp)
	if got["subject"] != "bearer" {
		t.Fatalf("principal missing: %#v", got)
	}
}

func TestHTTPBasicRejectsInvalidCredentials(t *testing.T) {
	app := New()
	app.Use(HTTPBasic(BasicConfig{Username: "user", Password: "pass"}))
	app.GET("/private", func(ctx *Context) (any, error) { return OK("ok"), nil })
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/private")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate")
	}
}

func TestRouterIncludeWithoutOptionsAndMethodsSharePath(t *testing.T) {
	sub := NewRouter()
	sub.AddRoute("GET", "/healthz", func(ctx *Context) (any, error) { return OK(map[string]string{"method": "get"}), nil })
	app := New()
	app.POST("/healthz", func(ctx *Context) (any, error) { return OK(map[string]string{"method": "post"}), nil })
	app.Include(sub)
	client := NewTestClient(app)
	defer client.Close()
	get, err := client.Get("/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, get)["method"]; got != "get" {
		t.Fatalf("GET returned %v", got)
	}
	postReq, _ := client.NewRequest(http.MethodPost, "/healthz", nil)
	post, err := client.Do(postReq)
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, post)["method"]; got != "post" {
		t.Fatalf("POST returned %v", got)
	}
}

func TestPathCatchAllConverter(t *testing.T) {
	app := New()
	app.GET("/files/{name:path}", func(ctx *Context) (any, error) { return OK(map[string]string{"name": ctx.Param("name")}), nil })
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/files/a/b/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, resp)["name"]; got != "a/b/c.txt" {
		t.Fatalf("got=%v", got)
	}
}

func TestStaticMountServesFilesAndPreventsTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	app := New()
	app.Mount("/static", NewStaticFiles(dir))
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/static/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello" {
		t.Fatalf("body=%q", body)
	}
	traversal, err := client.Get("/static/../secret")
	if err != nil {
		t.Fatal(err)
	}
	traversal.Body.Close()
	if traversal.StatusCode == http.StatusOK {
		t.Fatal("path traversal unexpectedly succeeded")
	}
}

func TestTestClientFormAndMultipart(t *testing.T) {
	app := New()
	app.POST("/form", func(ctx *Context) (any, error) { return OK(map[string]string{"name": ctx.FormValue("name")}), nil })
	client := NewTestClient(app)
	defer client.Close()
	form, err := client.PostForm("/form", url.Values{"name": {"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, form)["name"]; got != "alice" {
		t.Fatalf("form=%v", got)
	}
}

func TestOnErrorHandlerIsInvoked(t *testing.T) {
	app := New()
	app.OnError(reflectTypeHTTPError(), func(ctx *Context, err error) error {
		ctx.SetHeader("Content-Type", "text/plain")
		ctx.SetStatus(http.StatusTeapot)
		_, _ = ctx.WriteString("handled")
		return nil
	})
	app.GET("/error", func(ctx *Context) (any, error) { return nil, NewHTTPError(418, "teapot", "short") })
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/error")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusTeapot || !strings.Contains(string(body), "handled") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func reflectTypeHTTPError() reflect.Type { return reflect.TypeOf((*HTTPError)(nil)) }

type transientRouteDependency struct{ ID int }

func TestRouteDependencyTransientIsResolvedOnlyOnce(t *testing.T) {
	app := New()
	var calls atomic.Int32
	if err := app.DI().Register(func() *transientRouteDependency {
		return &transientRouteDependency{ID: int(calls.Add(1))}
	}, Transient()); err != nil {
		t.Fatal(err)
	}
	app.GET("/dependency", func(ctx *Context, dep *transientRouteDependency) (any, error) {
		return OK(map[string]int{"id": dep.ID}), nil
	}, RouteOption{Dependencies: []Depends{{Dependency: (*transientRouteDependency)(nil)}}})

	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/dependency")
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, resp)
	if got["id"] != float64(1) || calls.Load() != 1 {
		t.Fatalf("transient dependency resolved more than once: response=%v calls=%d", got, calls.Load())
	}
}

type concreteProviderError struct{ message string }

func (e concreteProviderError) Error() string { return e.message }

type concreteErrorDependency struct{}

func TestDIProviderConcreteErrorDoesNotPanic(t *testing.T) {
	di := NewDIContainer()
	if err := di.Register(func() (*concreteErrorDependency, concreteProviderError) {
		return nil, concreteProviderError{message: "provider failed"}
	}, Singleton()); err != nil {
		t.Fatal(err)
	}
	_, err := di.ResolveE(reflect.TypeOf((*concreteErrorDependency)(nil)), nil)
	if err == nil || err.Error() != "provider failed" {
		t.Fatalf("expected concrete provider error, got %v", err)
	}
}

type concreteHandlerError struct{ message string }

func (e concreteHandlerError) Error() string { return e.message }

func TestHandlerConcreteErrorDoesNotPanic(t *testing.T) {
	app := New()
	app.GET("/error", func(ctx *Context) (any, concreteHandlerError) {
		return nil, concreteHandlerError{message: "handler failed"}
	})
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/error")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDIOverrideByType(t *testing.T) {
	type service struct{ Name string }
	di := NewDIContainer()
	if err := di.Register(func() *service { return &service{Name: "original"} }, Singleton()); err != nil {
		t.Fatal(err)
	}
	di.Override(reflect.TypeOf((*service)(nil)), &service{Name: "override"})
	got, err := di.ResolveE(reflect.TypeOf((*service)(nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.(*service).Name != "override" {
		t.Fatalf("override not applied: %#v", got)
	}
}

func TestDISingletonCleanupsRunInReverseInitializationOrder(t *testing.T) {
	type first struct{}
	type second struct{}
	var calls []string
	di := NewDIContainer()
	if err := di.Register(func() (*first, func()) {
		return &first{}, func() { calls = append(calls, "first") }
	}, Singleton()); err != nil {
		t.Fatal(err)
	}
	if err := di.Register(func() (*second, func()) {
		return &second{}, func() { calls = append(calls, "second") }
	}, Singleton()); err != nil {
		t.Fatal(err)
	}
	if _, err := di.ResolveE(reflect.TypeOf((*first)(nil)), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := di.ResolveE(reflect.TypeOf((*second)(nil)), nil); err != nil {
		t.Fatal(err)
	}
	if err := di.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"second", "first"}) {
		t.Fatalf("cleanup order=%v", calls)
	}
}

func TestServeStopsWhenContextIsCancelled(t *testing.T) {
	app := New()
	app.GET("/healthz", func(ctx *Context) (any, error) { return OK("ok"), nil })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, listener) }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(time.Second)
	for {
		resp, reqErr := client.Get("http://" + listener.Addr().String() + "/healthz")
		if reqErr == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not become ready: %v", reqErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after context cancellation")
	}
}

func TestRouterTrieFallsBackFromStaticPrefixToParameterRoute(t *testing.T) {
	app := New()
	app.GET("/users/me/profile", func(ctx *Context) (any, error) { return OK("static"), nil })
	app.GET("/users/{id}", func(ctx *Context, id string) (any, error) { return OK(map[string]string{"id": id}), nil })
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.Get("/users/me")
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, resp)
	if got["id"] != "me" {
		t.Fatalf("dynamic fallback failed: %#v", got)
	}
}

func TestGetRouteAlsoAnswersHeadWithoutBody(t *testing.T) {
	app := New()
	app.GET("/resource", func(ctx *Context) (any, error) { return OK(map[string]string{"value": "present"}), nil })
	client := NewTestClient(app)
	defer client.Close()
	req, err := client.NewRequest(http.MethodHead, "/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD status=%d body=%q", resp.StatusCode, body)
	}
}

type validatedCreatePayload struct {
	Name string `json:"name"`
}

func (p *validatedCreatePayload) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	return nil
}

func TestTypedJSONBodyAndValidate(t *testing.T) {
	app := New()
	app.POST("/typed", func(ctx *Context, payload validatedCreatePayload) (any, error) {
		return Created(map[string]string{"name": payload.Name}), nil
	})
	client := NewTestClient(app)
	defer client.Close()
	resp, err := client.PostJSON("/typed", []byte(`{"name":"Ada"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, err = client.PostJSON("/typed", []byte(`{"name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("validation status=%d", resp.StatusCode)
	}
}

func TestRateLimitEnforcesConfiguredWindow(t *testing.T) {
	app := New()
	app.GET("/limited", func(ctx *Context) (any, error) { return OK("ok"), nil }, RateLimit("2/minute"))
	client := NewTestClient(app)
	defer client.Close()
	for i := 0; i < 2; i++ {
		resp, err := client.Get("/limited")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status=%d", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
	resp, err := client.Get("/limited")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("expected rate limit response; status=%d headers=%v", resp.StatusCode, resp.Header)
	}
}

type cycleDependencyA struct{}
type cycleDependencyB struct{}

func TestDIDetectsCircularDependency(t *testing.T) {
	di := NewDIContainer()
	if err := di.Register(func(*cycleDependencyB) *cycleDependencyA { return &cycleDependencyA{} }, Singleton()); err != nil {
		t.Fatal(err)
	}
	if err := di.Register(func(*cycleDependencyA) *cycleDependencyB { return &cycleDependencyB{} }, Singleton()); err != nil {
		t.Fatal(err)
	}
	_, err := di.ResolveE(reflect.TypeOf((*cycleDependencyA)(nil)), nil)
	if err == nil || !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("expected dependency cycle error, got %v", err)
	}
}

func TestClearRequestScopePreservesApplicationRequestState(t *testing.T) {
	type requestScoped struct{ Value int }
	di := NewDIContainer()
	if err := di.Register(func() *requestScoped { return &requestScoped{Value: 7} }, Request()); err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	ctx.State().Set("user-value", "preserve")
	first, err := di.ResolveE(reflect.TypeOf((*requestScoped)(nil)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := di.ResolveE(reflect.TypeOf((*requestScoped)(nil)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("request scope did not cache the dependency")
	}
	di.ClearRequestScope(ctx)
	if value, ok := ctx.State().Get("user-value"); !ok || value != "preserve" {
		t.Fatalf("request state was unexpectedly cleared: value=%v ok=%v", value, ok)
	}
	third, err := di.ResolveE(reflect.TypeOf((*requestScoped)(nil)), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("ClearRequestScope did not clear the DI cache")
	}
}

type overrideCleanupService struct{ Value string }

func TestDIOverrideRunsPreviousSingletonCleanupExactlyOnce(t *testing.T) {
	di := NewDIContainer()
	var cleanupCalls atomic.Int32
	if err := di.Register(func() (*overrideCleanupService, func()) {
		return &overrideCleanupService{Value: "original"}, func() { cleanupCalls.Add(1) }
	}, Singleton()); err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf((*overrideCleanupService)(nil))
	original, err := di.ResolveE(typ, nil)
	if err != nil || original.(*overrideCleanupService).Value != "original" {
		t.Fatalf("original dependency=%v err=%v", original, err)
	}
	di.Override(typ, &overrideCleanupService{Value: "replacement"})
	if cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup calls after override=%d, want 1", cleanupCalls.Load())
	}
	replacement, err := di.ResolveE(typ, nil)
	if err != nil || replacement.(*overrideCleanupService).Value != "replacement" {
		t.Fatalf("replacement dependency=%v err=%v", replacement, err)
	}
	if err := di.Close(); err != nil {
		t.Fatal(err)
	}
	if cleanupCalls.Load() != 1 {
		t.Fatalf("cleanup calls after close=%d, want 1", cleanupCalls.Load())
	}
}

func TestCLIHealthCommandUsesRegisteredChecks(t *testing.T) {
	app := New()
	cli := NewCLI(app, nil)
	if code := cli.healthCmd(nil); code == 0 {
		t.Fatal("health command reported not-ready app as healthy")
	}
	app.Health().SetReady(true)
	app.Health().RegisterChecker("database", func() error { return nil })
	if code := cli.healthCmd(nil); code != 0 {
		t.Fatalf("health command for healthy app returned %d", code)
	}
	app.Health().RegisterChecker("cache", func() error { return errors.New("unavailable") })
	if code := cli.healthCmd(nil); code == 0 {
		t.Fatal("health command reported failed dependency as healthy")
	}
}
