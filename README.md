# GoFastAPI

<img src="docs/LogoGoFastApi.png" alt="GoFastAPI Logo" width="120" align="right">

**O framework web para Go com paridade funcional FastAPI + servidor de produção Uvicorn, em um único binário.**

GoFastAPI traz a ergonomia do FastAPI e a robustez operacional do Uvicorn para o ecossistema Go — sem ASGI, sem decorators, sem asyncio. Apenas código Go idiomático, `net/http` sob medida e desempenho de produção desde o primeiro request.

---

## Por que GoFastAPI?

| FastAPI | Uvicorn | GoFastAPI |
|---------|---------|-----------|
| Ergonomia de desenvolvedor | Servidor de produção integrado | **Tudo em um único binário Go** |
| Rotas declarativas | Workers multiprocesso | **Trie de segmentos O(M) + supervisor nativo** |
| DI com escopos | Graceful shutdown | **DI por instância, graceful shutdown, sinais** |
| OpenAPI 3.1 automático | TLS, timeouts, limites | **OpenAPI 3.1, TLS/mTLS, Workers > 1, graceful** |
| Python (intérprete) | Python (intérprete) | **Go nativo: ~12–17 MB RSS, zero cold start** |

---

## Quick start

```go
package main

import (
    "log"
    gofast "gofast"
)

func main() {
    app := gofast.New(
        gofast.Title("Minha API"),
        gofast.Version("1.0.0"),
        gofast.Description("API de produção em Go, sem boilerplate"),
    )

    // Middleware de produção em uma linha
    app.Use(gofast.Recovery(nil))                    // panic → 500, sem expor stack
    app.Use(gofast.RequestID(""))                    // X-Request-ID para rastreamento
    app.Use(gofast.CORS(gofast.CORSConfig{           // CORS com preflight
        AllowOrigins: []string{"*"},
        AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    }))
    app.Use(gofast.Compression())                    // gzip automático
    app.Use(gofast.Timeout(10 * time.Second))        // timeout por requisição

    // Rotas com agrupamento (prefixo, tags, dependências compartilhadas)
    usuarios := app.Group("/usuarios", gofast.GroupTags("usuarios"))
    usuarios.GET("/{id}", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{"id": ctx.Param("id")}), nil
    })
    usuarios.POST("/", func(ctx *gofast.Context) (any, error) {
        return gofast.Created(map[string]string{"status": "criado"}), nil
    })

    // Método HTTP arbitrário
    app.Route("PROPFIND", "/arquivos/{id}", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{"method": "PROPFIND"}), nil
    })

    // DI com escopos: Singleton, Request, Transient
    app.Provide(novoBanco, gofast.Singleton())

    // Background tasks executadas após o flush da resposta
    app.POST("/emails", func(ctx *gofast.Context) (any, error) {
        ctx.BackgroundTasks().Add(func() {
            // envia e-mail após a resposta ir para o cliente
        })
        return gofast.OK(map[string]bool{"aceito": true}), nil
    })

    cfg := gofast.DefaultServerConfig()
    cfg.Addr = ":8000"
    cfg.Workers = 4          // supervisor multiprocesso nativo
    cfg.GracefulTimeout = 30 * time.Second

    log.Fatal(app.Run(cfg))
}
```

Execute:

```bash
go run ./cmd/server
```

Endpoints internos incluídos por padrão:

| Endpoint | Finalidade |
|----------|------------|
| `/openapi.json` | Contrato OpenAPI 3.1 |
| `/docs` | Swagger UI |
| `/redoc` | ReDoc |
| `/health/live` | Liveness |
| `/health/ready` | Readiness com checkers |

---

## Funcionalidades da biblioteca

### 1. Rotas e grupos

Registre rotas com métodos HTTP padrão ou arbitrários. Agrupe rotas com prefixo, tags e dependências compartilhadas.

```go
app.GET("/", handler)
app.POST("/items/{id}", handler)
app.Route("PROPFIND", "/arquivos/{id}", handler)

usuarios := app.Group("/usuarios", gofast.GroupTags("usuarios"))
usuarios.GET("/{id}", handler)
usuarios.POST("/", handler)

app.Include(router, gofast.IncludeOption{Prefix: "/v1"})
app.Mount("/static", gofast.NewStaticFiles("./public"))
```

- Rotas estáticas: busca O(1) por mapa.
- Rotas dinâmicas: busca O(M) por segmento via trie — sem varredura linear com regex.
- `{id}` captura segmento único; `{filepath:path}` captura o restante (incluindo barras).
- Middlewares por rota pré-compilados no registro.

### 2. Context e binding

O `Context` é o wrapper de requisição. Oferece `Param`, `Query`, `Header`, `Cookie`, `Session`, `State`, `Resolve`, `JSON`, `Body`, `FormValue`, `FormFile`, `RemoteAddr`, `SetHeader`, `SetStatus`, `Write`, `WriteString` e métodos auxiliares.

```go
app.POST("/items", func(ctx *gofast.Context, input CreateItem) (any, error) {
    return gofast.Created(map[string]string{"name": input.Name}), nil
})
```

### 3. Injeção de dependências (DI)

Cada aplicação possui seu próprio container DI (não global). Escopos: `Singleton`, `Request`, `Transient`.

```go
app.Provide(NewUserService, gofast.Singleton())

app.GET("/me", func(ctx *gofast.Context, svc *UserService) (any, error) {
    return gofast.OK(map[string]string{"state": "ready"}), nil
}, gofast.RouteOption{
    Dependencies: []gofast.Depends{{Dependency: (*UserService)(nil)}},
})
```

Providers podem retornar `T`, `(T, error)`, `(T, func())` ou `(T, func() error)`. Dependências cíclicas são detectadas. Cleanups request/transient são executados ao fim da requisição; cleanups singleton no fechamento da aplicação.

### 4. Respostas e erros

Tipos disponíveis: `OK`, `Created`, `NewJSONResponse`, `NewTextResponse`, `NewHTMLResponse`, `NewFileResponse`, `NewRedirectResponse`, `NewStreamResponse`, `NewEmptyResponse`.

Erros podem ser tratados com `HTTPError`, `ValidationError` e `app.OnError(typeOrPredicate, handler)`.

### 5. Middleware

O tipo base é `func(http.Handler) http.Handler`. Registre middleware global com `app.Use(...)`.

Middlewares disponíveis: `AccessLog`, `Compression`, `CORS`, `BodyLimit`, `ConcurrencyLimit`, `MaxConnections`, `Proxy`, `TrustedHost`, `HTTPSRedirect`, `Recovery`, `RequestID`, `Timeout`, `RateLimit`.

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

### 6. Segurança

Cookie de sessão AES-GCM, APIKey (header/query/cookie), HTTPBearer, HTTPBasic, OAuth2Bearer. Os dados do usuário autenticado ficam em `ctx.Principal()`.

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

### 7. OpenAPI e documentação

`app.OpenAPI()` gera um documento OpenAPI 3.1. Rotas internas: `/openapi.json`, `/docs`, `/redoc`.

```go
app.AddSecurityScheme("BearerAuth", gofast.SecurityScheme{
    Type:             "http",
    Scheme:           "bearer",
    BearerFormat:     "JWT",
    Description:      "Autenticação via Bearer token JWT",
})
```

### 8. WebSocket e streaming

WebSocket com handshake/upgrade e frames básicos. SSE e streaming genérico com `SSE`, `SSEWithHeartbeat`, `WriteEvent`, `Stream`, `StreamWithContentType`.

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

Tarefas em processo, executadas sequencialmente após o flush da resposta.

```go
app.POST("/emails", func(ctx *gofast.Context) (any, error) {
    ctx.BackgroundTasks().Add(func() {
        // envia e-mail após a resposta
    })
    return gofast.OK(map[string]bool{"queued": true}), nil
})
```

### 10. Health checks

`/health/live` e `/health/ready` com checkers registrados.

```go
app.Health().RegisterChecker("database", func() error {
    return db.PingContext(context.Background())
})
```

### 11. Servidor de produção (Uvicorn-like)

`app.Run()` incorpora o papel operacional do Uvicorn. Suporte a TLS/mTLS, timeouts, limites, proxy, graceful shutdown com sinais, Workers > 1 via supervisor nativo (listener herdado por FD), HTTP/2 e h2c.

```go
cfg := gofast.DefaultServerConfig()
cfg.Addr = ":8000"
cfg.Workers = 4
cfg.GracefulTimeout = 30 * time.Second
cfg.CertFile = "cert.pem"
cfg.KeyFile = "key.pem"

log.Fatal(app.Run(cfg))
```

### 12. Observabilidade

Pacote `observability` com `Counter`, `Gauge`, `Histogram`, `Metrics` e middleware de instrumentação.

```go
import "gofast/observability"

obs := observability.NewObservability(true)
app.Use(obs.Middleware())
```

### 13. Configuração

Pacote `config` com carregamento por precedência: defaults > arquivo JSON > variáveis de ambiente `GOFAST_*` > CLI.

```go
import "gofast/config"

cfg := config.MustLoad(
    config.WithEnvPrefix("GOFAST_"),
    config.WithConfigFile("config.json"),
    config.WithCLIOverrides(map[string]any{"Workers": 4}),
)
app.Run(*cfg)
```

### 14. Hot Reload

Quando `cfg.Reload = true`, o servidor monitora arquivos `.go` nos diretórios configurados e reinicia automaticamente quando uma mudança é detectada. Útil para desenvolvimento (`gofast dev --reload`).

```go
cfg.Reload = true
cfg.ReloadDirs = []string{"."}
cfg.ReloadDelay = 1 * time.Second
```

---

## Testes e qualidade

```bash
go test ./...
go test -race ./...
go vet ./...
go test ./... -cover
go test -run=^$ -bench=. -benchmem
go test -run=^$ -fuzz=FuzzTrieLookup -fuzztime=10s
```

Fuzz targets incluídos: extração de parâmetros, parser de RateLimit, lookup de trie.

---

## Métricas (microbenchmarks internos, não RPS TCP)

| Cenário | ns/op | B/op | allocs/op |
|---------|-------|------|-----------|
| Handler HTTP direto | 2.330 | 1.232 | 16 |
| GoFastAPI rota estática | 2.571 | 1.488 | 19 |
| GoFastAPI rota dinâmica | 2.914 | 2.416 | 23 |
| GoFastAPI JSON response | 1.015 | 352 | 9 |

> Ambiente: Linux/AMD64, Go 1.23.2, 2 vCPUs efetivas, 4 GiB RAM. Microbenchmarks de `testing.B` em processo — não substituem testes de carga TCP com gerador externo.

---

## Documentação de arquitetura

- [SDD — Software Design Document](SDD_GoFastAPI_Production_Server.md): especificação completa, matriz de rastreabilidade e roadmap por fases
- [Documentação HTML](docs/index.html): referência técnica completa e benchmarks comparativos vs FastAPI
- [Exemplo executável](cmd/server/main.go): aplicação completa com middlewares, DI, grupos e servidor

---

## Licença

Consulte [LICENSE.md](LICENSE.md).