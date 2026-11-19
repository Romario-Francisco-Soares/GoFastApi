# GoFastAPI

<img src="docs/LogoGoFastApi.png" alt="GoFastAPI Logo" width="120" align="right">

**The Go web framework with FastAPI feature parity + a Uvicorn-class production server, in a single binary.**

GoFastAPI brings FastAPI's developer ergonomics and Uvicorn's operational robustness to the Go ecosystem — no ASGI, no decorators, no asyncio. Just idiomatic Go code, a tailored `net/http`, and production-grade performance from the very first request.

---

## Why GoFastAPI?

| FastAPI | Uvicorn | GoFastAPI |
|---------|---------|-----------|
| Developer ergonomics | Built-in production server | **Everything in a single Go binary** |
| Declarative routes | Multi-process workers | **O(M) segment trie + native supervisor** |
| Scoped DI | Graceful shutdown | **Per-instance DI, graceful shutdown, signals** |
| Automatic OpenAPI 3.1 | TLS, timeouts, limits | **OpenAPI 3.1, TLS/mTLS, Workers > 1, graceful** |
| Python (interpreter) | Python (interpreter) | **Native Go: ~12–17 MB RSS, zero cold start** |

---

## Installation

Requires Go 1.23 or newer.

```bash
go get github.com/Romario-Francisco-Soares/GoFastApi@latest
```

To run the example server:

```bash
go install github.com/Romario-Francisco-Soares/GoFastApi/cmd/server@latest
```

The first public release is `v0.1.0`. Pin a version in production with `@v0.1.x`.

---

## Quick start

```go
package main

import (
    "log"
    gofast "github.com/Romario-Francisco-Soares/GoFastApi"
)

func main() {
    app := gofast.New(
        gofast.Title("My API"),
        gofast.Version("1.0.0"),
        gofast.Description("A production API in Go, without boilerplate"),
    )

    // Production middleware in one line
    app.Use(gofast.Recovery(nil))                    // panic → 500, no stack exposed
    app.Use(gofast.RequestID(""))                    // X-Request-ID for tracing
    app.Use(gofast.CORS(gofast.CORSConfig{           // CORS with preflight
        AllowOrigins: []string{"*"},
        AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    }))
    app.Use(gofast.Compression())                    // automatic gzip
    app.Use(gofast.Timeout(10 * time.Second))        // per-request timeout

    // Routes with grouping (prefix, tags, shared dependencies)
    users := app.Group("/users", gofast.GroupTags("users"))
    users.GET("/{id}", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{"id": ctx.Param("id")}), nil
    })
    users.POST("/", func(ctx *gofast.Context) (any, error) {
        return gofast.Created(map[string]string{"status": "created"}), nil
    })

    // Arbitrary HTTP method
    app.Route("PROPFIND", "/files/{id}", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{"method": "PROPFIND"}), nil
    })

    // Scoped DI: Singleton, Request, Transient
    app.Provide(newDatabase, gofast.Singleton())

    // Background tasks run after the response is flushed
    app.POST("/emails", func(ctx *gofast.Context) (any, error) {
        ctx.BackgroundTasks().Add(func() {
            // send the e-mail after the response reaches the client
        })
        return gofast.OK(map[string]bool{"accepted": true}), nil
    })

    cfg := gofast.DefaultServerConfig()
    cfg.Addr = ":8000"
    cfg.Workers = 4          // native multi-process supervisor
    cfg.GracefulTimeout = 30 * time.Second

    log.Fatal(app.Run(cfg))
}
```

Run:

```bash
go run ./cmd/server
```

Built-in endpoints enabled by default:

| Endpoint | Purpose |
|----------|---------|
| `/openapi.json` | OpenAPI 3.1 contract |
| `/docs` | Swagger UI |
| `/redoc` | ReDoc |
| `/health/live` | Liveness |
| `/health/ready` | Readiness with checkers |

---

## Library features

### 1. Routes and groups

Register routes with standard or arbitrary HTTP methods. Group routes with a prefix, tags, and shared dependencies.

```go
app.GET("/", handler)
app.POST("/items/{id}", handler)
app.Route("PROPFIND", "/files/{id}", handler)

users := app.Group("/users", gofast.GroupTags("users"))
users.GET("/{id}", handler)
users.POST("/", handler)

app.Include(router, gofast.IncludeOption{Prefix: "/v1"})
app.Mount("/static", gofast.NewStaticFiles("./public"))
```

- Static routes: O(1) lookup via a map.
- Dynamic routes: O(M) per-segment lookup through a trie — no linear regex scans.
- `{id}` captures a single segment; `{filepath:path}` captures the rest (including slashes).
- Per-route middleware is pre-compiled at registration time.

### 2. Context and binding

`Context` is the request wrapper. It provides `Param`, `Query`, `Header`, `Cookie`, `Session`, `State`, `Resolve`, `JSON`, `Body`, `FormValue`, `FormFile`, `RemoteAddr`, `SetHeader`, `SetStatus`, `Write`, `WriteString`, and helper methods.

```go
app.POST("/items", func(ctx *gofast.Context, input CreateItem) (any, error) {
    return gofast.Created(map[string]string{"name": input.Name}), nil
})
```

### 3. Dependency injection (DI)

Each application has its own DI container (not global). Scopes: `Singleton`, `Request`, `Transient`.

```go
app.Provide(NewUserService, gofast.Singleton())

app.GET("/me", func(ctx *gofast.Context, svc *UserService) (any, error) {
    return gofast.OK(map[string]string{"state": "ready"}), nil
}, gofast.RouteOption{
    Dependencies: []gofast.Depends{{Dependency: (*UserService)(nil)}},
})
```

Providers may return `T`, `(T, error)`, `(T, func())`, or `(T, func() error)`. Circular dependencies are detected. Request/transient cleanups run at the end of the request; singleton cleanups run when the application shuts down.

### 4. Responses and errors

Available types: `OK`, `Created`, `NewJSONResponse`, `NewTextResponse`, `NewHTMLResponse`, `NewFileResponse`, `NewRedirectResponse`, `NewStreamResponse`, `NewEmptyResponse`.

Errors can be handled with `HTTPError`, `ValidationError`, and `app.OnError(typeOrPredicate, handler)`.

### 5. Middleware

The base type is `func(http.Handler) http.Handler`. Register global middleware with `app.Use(...)`.

Available middleware: `AccessLog`, `Compression`, `CORS`, `BodyLimit`, `ConcurrencyLimit`, `MaxConnections`, `Proxy`, `TrustedHost`, `HTTPSRedirect`, `Recovery`, `RequestID`, `Timeout`, `RateLimit`.

```go
app.Use(gofast.Recovery(nil))
app.Use(gofast.RequestID(""))
app.Use(gofast.CORS(gofast.CORSConfig{
    AllowOrigins: []string{"*"},
    AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
}))
app.Use(gofast.Compression())
app.Use(gofast.Timeout(10 * time.Second))
```

### 6. Security

AES-GCM session cookie, APIKey (header/query/cookie), HTTPBearer, HTTPBasic, OAuth2Bearer. Authenticated user data is available at `ctx.Principal()`.

```go
app.Use(gofast.SessionMiddleware(gofast.SessionConfig{
    Secret:   os.Getenv("SESSION_SECRET"),
    CookieName: "session",
    HTTPOnly: true,
    Secure: true,
    SameSite: http.SameSiteLaxMode,
}))

app.Use(gofast.HTTPBearer(gofast.BearerConfig{
    Secret: "test-token",
}))
```

### 7. OpenAPI and documentation

`app.OpenAPI()` generates an OpenAPI 3.1 document. Built-in routes: `/openapi.json`, `/docs`, `/redoc`.

```go
app.AddSecurityScheme("BearerAuth", gofast.SecurityScheme{
    Type:             "http",
    Scheme:           "bearer",
    BearerFormat:     "JWT",
    Description:      "Bearer JWT token authentication",
})
```

### 8. WebSocket and streaming

WebSocket with handshake/upgrade and basic frames. SSE and generic streaming with `SSE`, `SSEWithHeartbeat`, `WriteEvent`, `Stream`, `StreamWithContentType`.

```go
app.GET("/ws", func(ctx *gofast.Context, conn gofast.WebSocketConn) error {
    for {
        msg, err := conn.ReadJSON()
        if err != nil {
            return err
        }
        if err := conn.WriteJSON(msg); err != nil {
            return err
        }
    }
})
```

### 9. Background tasks

In-process tasks, executed sequentially after the response is flushed.

```go
app.POST("/emails", func(ctx *gofast.Context) (any, error) {
    ctx.BackgroundTasks().Add(func() {
        // send the e-mail after the response
    })
    return gofast.OK(map[string]bool{"queued": true}), nil
})
```

### 10. Health checks

`/health/live` and `/health/ready` with registered checkers.

```go
app.Health().RegisterChecker("database", func() error {
    return db.PingContext(context.Background())
})
```

### 11. Production server (Uvicorn-like)

`app.Run()` takes on Uvicorn's operational role. It supports TLS/mTLS, timeouts, limits, proxy handling, graceful shutdown with signals, and Workers > 1 through a native supervisor (listener inherited via FD), plus HTTP/2 and h2c.

```go
cfg := gofast.DefaultServerConfig()
cfg.Addr = ":8000"
cfg.Workers = 4
cfg.GracefulTimeout = 30 * time.Second
cfg.CertFile = "cert.pem"
cfg.KeyFile = "key.pem"

log.Fatal(app.Run(cfg))
```

### 12. Observability

The `observability` package provides `Counter`, `Gauge`, `Histogram`, `Metrics`, and instrumentation middleware.

```go
import "github.com/Romario-Francisco-Soares/GoFastApi/observability"

obs := observability.NewObservability(true)
app.Use(obs.Middleware())
```

### 13. Configuration

The `config` package loads settings by precedence: defaults > JSON file > `GOFAST_*` environment variables > CLI.

```go
import "github.com/Romario-Francisco-Soares/GoFastApi/config"

cfg := config.MustLoad(
    config.WithEnvPrefix("GOFAST_"),
    config.WithConfigFile("config.json"),
    config.WithCLIOverrides(map[string]any{"Workers": 4}),
)
app.Run(*cfg)
```

### 14. Hot Reload

When `cfg.Reload = true`, the server watches `.go` files in the configured directories. On a change it rebuilds the application with `go build`, shuts the running server down gracefully, and starts the new binary in a fresh process. If compilation fails, the current server keeps running and the error is logged. `gofast dev` enables reload by default; use `--no-reload` to disable it.

```go
cfg.Reload = true
cfg.ReloadDirs = []string{"."}
cfg.ReloadDelay = 1 * time.Second
cfg.ReloadPackage = "./cmd/server" // optional; auto-detected from ./... when empty
```

---

## Testing and quality

```bash
go test ./...
go test -race ./...
go vet ./...
go test ./... -cover
go test -run=^$ -bench=. -benchmem
go test -run=^$ -fuzz=FuzzTrieLookup -fuzztime=10s
```

Included fuzz targets: parameter extraction, RateLimit parser, and trie lookup.

---

## Metrics (internal microbenchmarks, not TCP RPS)

| Scenario | ns/op | B/op | allocs/op |
|----------|-------|------|-----------|
| Direct HTTP handler | 2,330 | 1,232 | 16 |
| GoFastAPI static route | 2,571 | 1,488 | 19 |
| GoFastAPI dynamic route | 2,914 | 2,416 | 23 |
| GoFastAPI JSON response | 1,015 | 352 | 9 |

> Environment: Linux/AMD64, Go 1.23.2, 2 effective vCPUs, 4 GiB RAM. In-process `testing.B` microbenchmarks — they do not replace TCP load tests with an external generator.

---

## Architecture documentation

- Software Design Document: internal specification (not distributed with the module)
- [HTML documentation](docs/index.html): complete technical reference and comparative benchmarks vs FastAPI
- [Runnable example](cmd/server/main.go): a complete application with middleware, DI, groups, and the server

---

## License

See [LICENSE.md](LICENSE.md).
