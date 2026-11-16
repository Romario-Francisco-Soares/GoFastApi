# GoFastAPI

**Uma experiência de desenvolvimento inspirada no FastAPI, construída sobre `net/http` e o runtime Go.**

GoFastAPI oferece roteamento, contexto de requisição, respostas HTTP, middleware, DI, documentação OpenAPI, sessões e ferramentas de teste em uma API de alto nível, preservando a interoperabilidade com o ecossistema Go.

> **Estado do projeto:** em desenvolvimento. A biblioteca é funcional para APIs HTTP, mas ainda não oferece paridade integral com FastAPI/Uvicorn. Consulte a matriz de recursos e as limitações antes de adotar em produção.

---

## Sumário

- [Destaques](#destaques)
- [Instalação](#instalação)
- [Quick start](#quick-start)
- [Handlers e binding](#handlers-e-binding)
- [Rotas, grupos e middleware](#rotas-grupos-e-middleware)
- [Injeção de dependências](#injeção-de-dependências)
- [Respostas, erros e background tasks](#respostas-erros-e-background-tasks)
- [Sessões e autenticação](#sessões-e-autenticação)
- [OpenAPI e documentação](#openapi-e-documentação)
- [Servidor e configuração](#servidor-e-configuração)
- [Matriz de funcionalidades](#matriz-de-funcionalidades)
- [Testes e benchmarks](#testes-e-benchmarks)
- [Limitações conhecidas](#limitações-conhecidas)
- [Documentação de arquitetura](#documentação-de-arquitetura)
- [Licença](#licença)

## Destaques

- API de rotas inspirada em frameworks de alto nível.
- Construída sobre `net/http`; `App.Handler()` pode ser integrado a outros servidores e middlewares Go.
- Rotas estáticas indexadas e rotas dinâmicas em trie segmentada.
- Context com inicialização lazy de parâmetros, sessão e estado.
- Respostas JSON, texto, HTML, arquivo, redirect, stream e vazia.
- DI por aplicação com `Singleton`, `Request` e `Transient`.
- Middleware para CORS, gzip, proxy, trusted host, recovery, limites, timeout e request ID.
- Sessões stateless criptografadas com AES-GCM.
- API Key, HTTP Basic e HTTP Bearer com callback de validação.
- OpenAPI 3.1 básico, Swagger UI e ReDoc.
- Supervisor multiprocesso com handshake de prontidão e restart policy.
- TestClient, testes de concorrência, fuzz targets e microbenchmarks.

## Instalação

O `go.mod` deste snapshot ainda usa o módulo local `gofast`. Para uso no mesmo repositório:

```bash
go test ./...
```

Antes de publicar/consumir externamente, substitua `module gofast` pelo caminho canônico do repositório e publique uma versão/tag. Depois disso, o consumidor poderá importar o módulo pelo endereço Git.

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
        gofast.Description("API demonstrativa com GoFastAPI"),
    )

    app.GET("/", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{
            "message": "Hello from GoFastAPI",
        }), nil
    })

    app.GET("/items/{item_id}", func(ctx *gofast.Context) (any, error) {
        return gofast.OK(map[string]string{
            "item_id": ctx.Param("item_id"),
            "search":  ctx.Query("q"),
        }), nil
    })

    cfg := gofast.DefaultServerConfig()
    cfg.Addr = ":8000"
    if err := app.Run(cfg); err != nil {
        log.Fatal(err)
    }
}
```

Execute:

```bash
go run ./cmd/server
```

Endpoints internos incluídos por padrão:

| Endpoint | Finalidade |
|---|---|
| `/openapi.json` | Documento OpenAPI |
| `/docs` | Swagger UI |
| `/redoc` | ReDoc |
| `/health/live` | Liveness |
| `/health/ready` | Readiness e health checkers |

## Handlers e binding

A forma recomendada para o caminho HTTP mais direto é:

```go
app.GET("/ping", func(ctx *gofast.Context) (any, error) {
    return gofast.OK(map[string]string{"status": "ok"}), nil
})
```

O Context oferece `Param`, `Query`, `Header`, `Cookie`, `JSON`, `Body`, `FormValue`, `FormFile`, `Session`, `State`, `Resolve` e demais helpers HTTP.

Também há suporte a handler tipado com corpo JSON:

```go
type CreateUser struct {
    Name string `json:"name"`
}

func (p *CreateUser) Validate() error {
    if p.Name == "" {
        return fmt.Errorf("name is required")
    }
    return nil
}

app.POST("/users", func(ctx *gofast.Context, input CreateUser) (any, error) {
    return gofast.Created(map[string]string{"name": input.Name}), nil
})
```

O binder tipado é intencionalmente limitado: aceita um corpo JSON por handler e `Validate() error` opcional. Não é equivalente ao Pydantic e não oferece binding automático completo por tags para headers, cookies, forms ou arquivos.

## Rotas, grupos e middleware

Métodos disponíveis em `App`: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `OPTIONS`, `HEAD`, `TRACE` e `WebSocket`.

```go
api := gofast.NewRouter()
users := api.Group("/users", gofast.GroupTags("users"))
users.GET("/{id}", func(ctx *gofast.Context) (any, error) {
    return gofast.OK(map[string]string{"id": ctx.Param("id")}), nil
})
app.Include(api, gofast.IncludeOption{Prefix: "/v1"})
```

Middleware externo usa a assinatura padrão Go:

```go
app.Use(middleware.Recovery(nil))
```

O pacote `gofast/middleware` disponibiliza AccessLog, Compression, CORS, BodyLimit, ConcurrencyLimit, MaxConnections, Proxy, TrustedHost, HTTPSRedirect, Recovery, RequestID e Timeout. Rate limit por IP é fornecido no pacote raiz por `gofast.RateLimit("100/minute")`.

## Injeção de dependências

Cada aplicação possui seu próprio container DI.

```go
type UserService struct{}

func NewUserService() *UserService { return &UserService{} }

app.Provide(NewUserService, gofast.Singleton())

app.GET("/me", func(ctx *gofast.Context, svc *UserService) (any, error) {
    return gofast.OK(map[string]string{"service": "available"}), nil
}, gofast.RouteOption{
    Dependencies: []gofast.Depends{{Dependency: (*UserService)(nil)}},
})
```

Escopos: `Singleton()` (por container/processo), `Request()` (por Context) e `Transient()` (nova resolução). Providers aceitam retornos `T`, `(T, error)`, `(T, func())` e `(T, func() error)`. Ciclos são detectados; cleanups Request/Transient são executados no fim da requisição e os Singleton no fechamento da aplicação.

## Respostas, erros e background tasks

Responses incluem `OK`, `Created`, `NewJSONResponse`, `NewTextResponse`, `NewHTMLResponse`, `NewFileResponse`, `NewRedirectResponse`, `NewStreamResponse` e `NewEmptyResponse`.

O tratamento de erro pode usar `HTTPError`, `ValidationError` e `app.OnError(...)`.

```go
app.GET("/notify", func(ctx *gofast.Context) (any, error) {
    ctx.BackgroundTasks().Add(func() {
        // tarefa curta, em processo
    })
    return gofast.OK(map[string]bool{"accepted": true}), nil
})
```

Background tasks são executadas após o flush da resposta, sequencialmente no processo que atende a requisição. Não são persistentes nem substituem uma fila de jobs.

## Sessões e autenticação

Sessões podem ser habilitadas com cookie AES-GCM:

```go
app.Use(gofast.SessionMiddleware(gofast.SessionConfig{
    Secret:   os.Getenv("SESSION_SECRET"),
    CookieName: "session",
    HTTPOnly: true,
    Secure: true, // em HTTPS
    SameSite: http.SameSiteLaxMode,
}))
```

O segredo deve ser aleatório, estável e mantido fora do código-fonte; o cookie tem limite prático de tamanho. Helpers de autenticação incluem `APIKey`, `HTTPBearer`, `HTTPBasic` e `OAuth2Bearer` (validação delegada a callback/provedor externo). `ctx.Principal()` recupera a identidade validada.

A biblioteca não emite JWT nem implementa servidor OAuth2/OIDC.

## OpenAPI e documentação

`app.OpenAPI()` gera um documento OpenAPI 3.1 básico. `RouteOption` permite descrever tags, summary, description, operation ID, responses, deprecated e security. `AddSecurityScheme` registra security schemes.

A geração de schemas de request/response não é uma inferência completa de tipos Go; `ResponseModel` é atualmente metadado, não um gerador completo de JSON Schema. WebSocket e Mount não são operações HTTP documentadas no OpenAPI.

## Servidor e configuração

```go
cfg := gofast.DefaultServerConfig()
cfg.Addr = ":8000"
cfg.ReadHeaderTimeout = 5 * time.Second
cfg.IdleTimeout = 60 * time.Second
cfg.GracefulTimeout = 30 * time.Second
cfg.Workers = 1

if err := app.Run(cfg); err != nil {
    log.Fatal(err)
}
```

O modo single-process usa `net/http.Server`, bloqueia enquanto o servidor está ativo e suporta shutdown graceful, sinais, TCP/UDS/FD, TLS (incluindo client CA/mTLS), timeouts, keep-alive, limites, proxy headers e trusted hosts.

`Workers > 1` inicia Supervisor multiprocesso com listener herdado e handshake de prontidão. O modo multiprocesso é dependente de processos e file descriptors, e o caminho TLS multiprocesso não está implementado; use TLS no reverse proxy nesse cenário.

Hot reload (`Reload=true`), HTTP/2 explícito e h2c são rejeitados com erro. O comando CLI `dev` inicia em debug, mas não observa arquivos nem recarrega o processo.

## CLI integrada

A CLI é executada dentro do processo da aplicação por `app.RunCLI(os.Args[1:])` (ou `gofast.RunCLI`). Comandos disponíveis: `run`, `dev`, `openapi`, `routes`, `version` e `health`. O comando `dev` ativa logging de desenvolvimento, mas não oferece hot reload. `health` consulta liveness, readiness e os health checkers; `routes` lista método, caminho e operation ID/summary.

## Matriz de funcionalidades

| Área | Status |
|---|---|
| HTTP routing estático/dinâmico | ✅ Implementado |
| Router groups/include/mount | ✅ Implementado |
| Context helpers e lazy initialization | ✅ Implementado |
| JSON body tipado e `Validate()` | ✅ Implementado no escopo documentado |
| DI Singleton/Request/Transient e cleanup | ✅ Implementado |
| HTTP responses e error handling | ✅ Implementado |
| Middleware CORS/gzip/proxy/recovery/limits | ✅ Implementado |
| API Key/Basic/Bearer e encrypted session | ✅ Implementado |
| OpenAPI 3.1 básico | 🟡 Parcial: sem inferência completa de schemas |
| Process supervisor | 🟡 Parcial: sem heartbeat ativo; TLS multiprocesso ausente |
| WebSocket | 🟡 Básico: sem fragmentação/subprotocol/compressão |
| OTel | 🟡 Scaffold no-op; sem exporter |
| Background tasks | 🟡 In-process e não duráveis |
| Hot reload | ❌ Não implementado |
| HTTP/2 e h2c no servidor | ❌ Não implementado |
| Servidor OAuth2/JWT | ❌ Não implementado |
| TestClient WS/SSE | ❌ Não implementado |

Legenda: ✅ funcional no escopo descrito; 🟡 limitado/parcial; ❌ ausente.

## Testes e benchmarks

```bash
go test ./...
go test -race ./...
go vet ./...
go test ./... -cover
go test -run=^$ -bench=. -benchmem
```

Fuzz targets presentes: extração de parâmetros, parser de RateLimit e lookup de trie. Exemplo:

```bash
go test -run=^$ -fuzz=FuzzTrieLookup -fuzztime=10s
```

Benchmarks internos são microbenchmarks de `testing.B` em processo, não testes de capacidade TCP. Para obter dados de throughput/latência de servidor, utilize um gerador externo e registre CPU, memória, SO/kernel, versão Go, workers, conexões, duração, payload, Keep-Alive e percentis de latência.

### Resultados de validação deste snapshot

Execuções sem cache após as correções:

```text
go test -count=1 ./...       PASS
go test -count=1 -race ./... PASS
go vet ./...                PASS
```

Cobertura medida: `gofast` 33,9%; `gofast/middleware` 21,3%; `gofast/cmd/server` 0,0% (sem testes diretos). Uma campanha fuzz explícita ultrapassou o limite de execução deste ambiente e foi interrompida; os fuzz targets e seeds existem, mas não se declara uma campanha prolongada aprovada.

### Microbenchmarks Go (não são RPS TCP)

Ambiente: Linux/AMD64, Go 1.23.2, Intel Xeon Platinum 8573C; 3 CPUs lógicas visíveis, quota cgroup equivalente a 2 CPUs e limite de RAM de 4 GiB. Executado com `GOMAXPROCS=2` e `-benchtime=500ms`.

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| Handler HTTP direto | 2.330 | 1.232 | 16 |
| GoFastAPI rota estática | 2.571 | 1.488 | 19 |
| GoFastAPI rota dinâmica | 2.914 | 2.416 | 23 |
| GoFastAPI JSON response | 1.015 | 352 | 9 |

O handler estático da GoFastAPI ficou cerca de 10,3% acima do handler HTTP direto em tempo por operação neste microbenchmark específico. Não extrapole esse valor para throughput de rede ou cargas com middleware/DI. Os detalhes estão no SDD e em `benchmark_test.go`.

## Limitações conhecidas

Antes de produção, avalie especialmente:

1. Binding e schemas OpenAPI ainda não têm paridade com Pydantic/FastAPI.
2. O caminho de handlers tipados/DI usa reflexão por requisição; a assinatura simples `func(*Context) (any, error)` é a mais direta.
3. Telemetria OTel é no-op/scaffold.
4. Background tasks não são persistentes.
5. O supervisor não possui heartbeat periódico nem detector de worker travado.
6. Hot reload e HTTP/2/h2c não estão disponíveis.
7. WebSocket possui suporte básico, sem todas as extensões/protocolos.
8. `ServerConfig` contém campos reservados ainda sem efeito; consulte o SDD completo.
9. `go.mod` usa o módulo local `gofast`; substitua pelo caminho canônico do repositório antes da publicação.

## Documentação de arquitetura

- [SDD — Software Design Document](SDD_GoFastAPI_Production_Server.md)
- [Documentação HTML](gofastapi_documentation.html)
- [Exemplo executável](cmd/server/main.go)

## Licença

Consulte [LICENSE.md](LICENSE.md).
