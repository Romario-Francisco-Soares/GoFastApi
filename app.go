package gofast

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math"
	mathrand "math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Romario-Francisco-Soares/GoFastApi/hotreload"
	"github.com/Romario-Francisco-Soares/GoFastApi/middleware"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// App é a estrutura principal do GoFastAPI, equivalente à classe FastAPI do Python.
type App struct {
	mu                  sync.RWMutex
	router              *Router
	middleware          []Middleware
	onStart             []LifecycleHook
	onStop              []LifecycleHook
	exceptionHandlers   map[interface{}]ErrorHandler
	dependencyOverrides map[interface{}]interface{}
	state               *State
	serverConfig        *ServerConfig
	openAPIDoc          *OpenAPIDocument
	openAPIDocMu        sync.RWMutex
	title               string
	version             string
	description         string
	openapiURL          string
	docsURL             string
	redocURL            string
	healthManager       *HealthManager
	otel                *OTelManager
	cli                 *CLI
	di                  *DIContainer // Container DI por instância de App (não global)
	serverMu            sync.Mutex
	server              *http.Server
	stopOnce            sync.Once
	stopErr             error
	hotReloader         *hotreload.HotReloader // hot reload para desenvolvimento
}

// OpenAPIDocument representa um documento OpenAPI 3.1.
type OpenAPIDocument struct {
	OpenAPI    string                `json:"openapi"`
	Info       Info                  `json:"info"`
	Servers    []Server              `json:"servers,omitempty"`
	Paths      map[string]any        `json:"paths"`
	Components *Components           `json:"components,omitempty"`
	Security   []SecurityRequirement `json:"security,omitempty"`
	Tags       []Tag                 `json:"tags,omitempty"`
}

// Info contém informações da API.
type Info struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// Server contém informações do servidor.
type Server struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// Components contém componentes OpenAPI.
type Components struct {
	Schemas         map[string]any            `json:"schemas,omitempty"`
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`
}

// SecurityScheme define um esquema de segurança OpenAPI 3.1.
type SecurityScheme struct {
	Type             string      `json:"type"`
	Scheme           string      `json:"scheme,omitempty"`
	BearerFormat     string      `json:"bearerFormat,omitempty"`
	Name             string      `json:"name,omitempty"`
	In               string      `json:"in,omitempty"`
	Description      string      `json:"description,omitempty"`
	OpenIdConnectUrl string      `json:"openIdConnectUrl,omitempty"`
	Flows            *OAuthFlows `json:"flows,omitempty"`
}

// OAuthFlows define os fluxos OAuth2.
type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit,omitempty"`
	Password          *OAuthFlow `json:"password,omitempty"`
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

// OAuthFlow define um fluxo OAuth2.
type OAuthFlow struct {
	AuthorizationUrl string            `json:"authorizationUrl,omitempty"`
	TokenUrl         string            `json:"tokenUrl,omitempty"`
	RefreshUrl       string            `json:"refreshUrl,omitempty"`
	Scopes           map[string]string `json:"scopes,omitempty"`
}

// SecurityRequirement é um requisito de segurança.
type SecurityRequirement map[string][]string

// Tag é uma tag OpenAPI.
type Tag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Option é uma função de configuração para o App.
type Option func(*App)

// Title define o título da API (usado no OpenAPI).
func Title(t string) Option {
	return func(a *App) { a.title = t }
}

// Version define a versão da API.
func Version(v string) Option {
	return func(a *App) { a.version = v }
}

// Description define a descrição da API.
func Description(d string) Option {
	return func(a *App) { a.description = d }
}

// New cria uma nova instância do App.
func New(opts ...Option) *App {
	a := &App{
		router:              NewRouter(),
		middleware:          []Middleware{},
		onStart:             []LifecycleHook{},
		onStop:              []LifecycleHook{},
		exceptionHandlers:   make(map[interface{}]ErrorHandler),
		dependencyOverrides: make(map[interface{}]interface{}),
		state:               NewState(),
		title:               "GoFastAPI",
		version:             "0.1.0",
		description:         "",
		openapiURL:          "/openapi.json",
		docsURL:             "/docs",
		redocURL:            "/redoc",
		healthManager:       NewHealthManager(),
		otel:                NewOTelManager(TelemetryConfig{}, slog.Default()),
		di:                  NewDIContainer(), // DI container por instância de App
	}
	for _, opt := range opts {
		opt(a)
	}
	a.setup()
	return a
}

// setup configura rotas internas (OpenAPI, docs, redoc, health).
func (a *App) setup() {
	if a.openapiURL != "" {
		a.GET(a.openapiURL, func(ctx *Context) (any, error) {
			doc := a.OpenAPI()
			return NewJSONResponse(doc, StatusOK), nil
		}, RouteOption{ExcludeInSchema: true})
	}
	if a.openapiURL != "" && a.docsURL != "" {
		a.GET(a.docsURL, func(ctx *Context) (any, error) {
			return NewHTMLResponse(swaggerUIHTML(a.title, a.openapiURL)), nil
		}, RouteOption{ExcludeInSchema: true})
	}
	if a.openapiURL != "" && a.redocURL != "" {
		a.GET(a.redocURL, func(ctx *Context) (any, error) {
			return NewHTMLResponse(redocHTML(a.title, a.openapiURL)), nil
		}, RouteOption{ExcludeInSchema: true})
	}

	// Rotas de health check
	a.GET("/health/live", func(ctx *Context) (any, error) {
		if a.healthManager.IsLive() {
			return NewJSONResponse(map[string]string{"status": "live"}, StatusOK), nil
		}
		return NewJSONResponse(map[string]string{"status": "dead"}, http.StatusServiceUnavailable), nil
	}, RouteOption{ExcludeInSchema: true})
	a.GET("/health/ready", func(ctx *Context) (any, error) {
		if a.healthManager.IsReady() {
			for _, err := range a.healthManager.Check() {
				if err != nil {
					return NewJSONResponse(map[string]string{"status": "not ready"}, http.StatusServiceUnavailable), nil
				}
			}
			return NewJSONResponse(map[string]string{"status": "ready"}, StatusOK), nil
		}
		return NewJSONResponse(map[string]string{"status": "not ready"}, http.StatusServiceUnavailable), nil
	}, RouteOption{ExcludeInSchema: true})
}

// GET registra uma rota GET.
func (a *App) GET(path string, handler any, opts ...any) {
	a.router.AddRoute("GET", path, handler, opts...)
}

// POST registra uma rota POST.
func (a *App) POST(path string, handler any, opts ...any) {
	a.router.AddRoute("POST", path, handler, opts...)
}

// PUT registra uma rota PUT.
func (a *App) PUT(path string, handler any, opts ...any) {
	a.router.AddRoute("PUT", path, handler, opts...)
}

// PATCH registra uma rota PATCH.
func (a *App) PATCH(path string, handler any, opts ...any) {
	a.router.AddRoute("PATCH", path, handler, opts...)
}

// DELETE registra uma rota DELETE.
func (a *App) DELETE(path string, handler any, opts ...any) {
	a.router.AddRoute("DELETE", path, handler, opts...)
}

// OPTIONS registra uma rota OPTIONS.
func (a *App) OPTIONS(path string, handler any, opts ...any) {
	a.router.AddRoute("OPTIONS", path, handler, opts...)
}

// HEAD registra uma rota HEAD.
func (a *App) HEAD(path string, handler any, opts ...any) {
	a.router.AddRoute("HEAD", path, handler, opts...)
}

// TRACE registra uma rota TRACE.
func (a *App) TRACE(path string, handler any, opts ...any) {
	a.router.AddRoute("TRACE", path, handler, opts...)
}

// WebSocket registra uma rota de WebSocket.
func (a *App) WebSocket(path string, handler any, opts ...any) {
	a.router.AddWebSocket(path, handler, opts...)
}

// Use adiciona middleware global.
func (a *App) Use(mw ...Middleware) {
	a.middleware = append(a.middleware, mw...)
}

// Include inclui um router (sub-router).
func (a *App) Include(router *Router, opts ...IncludeOption) {
	a.router.Include(router, opts...)
}

// Mount monta um http.Handler em um caminho.
func (a *App) Mount(path string, h http.Handler) {
	a.router.Mount(path, h)
}

// RoutePaths retorna os caminhos das rotas registradas (para testes/debug).
func (a *App) RoutePaths() []string {
	a.router.mu.RLock()
	defer a.router.mu.RUnlock()
	paths := make([]string, 0, len(a.router.allRoutes))
	for _, r := range a.router.allRoutes {
		paths = append(paths, r.Path)
	}
	return paths
}

// RoutesCount retorna o número de rotas registradas (para testes/debug).
func (a *App) RoutesCount() int {
	a.router.mu.RLock()
	defer a.router.mu.RUnlock()
	return len(a.router.allRoutes)
}

// Route registra uma rota com método HTTP arbitrário (ex: PROPFIND, MKCOL, REPORT).
// Delegado ao Router subjacente, que aplica índice por método, trie dinâmica e
// compilação do HandlerPlan no registro (zero reflexão no hot path).
func (a *App) Route(method, path string, handler any, opts ...RouteOption) {
	anyOpts := make([]any, len(opts))
	for i, opt := range opts {
		anyOpts[i] = opt
	}
	a.router.AddRoute(strings.ToUpper(method), path, handler, anyOpts...)
}

// Group cria um grupo de rotas vinculado a este App, com prefixo, tags e
// dependências compartilhadas por todas as rotas registradas no sub-router.
// É um atalho para a.router.Group(prefix, opts...).
// As rotas registradas no sub-router são automaticamente mescladas ao router
// principal do App quando Handler() é chamado.
func (a *App) Group(prefix string, opts ...GroupOption) *Router {
	return a.router.Group(prefix, opts...)
}

// Provide registra uma dependência no container DI por instância de App.
func (a *App) Provide(provider any, opts ...ProvideOption) {
	a.di.Provide(provider, opts...)
}

// DI retorna o container DI por instância de App.
func (a *App) DI() *DIContainer {
	return a.di
}

// OnError registra um handler de exceção para um tipo específico.
func (a *App) OnError(typeOrPredicate any, handler ErrorHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exceptionHandlers[typeOrPredicate] = handler
}

// AddSecurityScheme registra um esquema de segurança no OpenAPI.
func (a *App) AddSecurityScheme(name string, scheme SecurityScheme) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.openAPIDoc == nil {
		a.openAPIDoc = NewOpenAPIDocument(a.title, a.version, a.description)
	}
	if a.openAPIDoc.Components == nil {
		a.openAPIDoc.Components = &Components{
			Schemas:         make(map[string]any),
			SecuritySchemes: make(map[string]SecurityScheme),
		}
	}
	a.openAPIDoc.Components.SecuritySchemes[name] = scheme
}

// Health retorna o gerenciador de health checks.
func (a *App) Health() *HealthManager {
	return a.healthManager
}

// OTel retorna o gerenciador OpenTelemetry.
func (a *App) OTel() *OTelManager {
	return a.otel
}

// CLI retorna a interface de linha de comando.
func (a *App) CLI() *CLI {
	if a.cli == nil {
		a.cli = NewCLI(a, os.Args[1:])
	}
	return a.cli
}

// RunCLI executa a CLI da aplicação.
func (a *App) RunCLI(args []string) int {
	cli := NewCLI(a, args)
	return cli.Run()
}

// OnStart registra um hook de inicialização (equivalente a lifespan startup).
func (a *App) OnStart(hook LifecycleHook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onStart = append(a.onStart, hook)
}

// OnStop registra um hook de finalização (equivalente a lifespan shutdown).
func (a *App) OnStop(hook LifecycleHook) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onStop = append(a.onStop, hook)
}

// DependencyOverride substitui uma dependência (para testes).
func (a *App) DependencyOverride(original, replacement interface{}) {
	a.di.Override(original, replacement)
}

// Handler retorna o http.Handler para interoperação com net/http.
// Middlewares globais são aplicados APENAS uma vez aqui pelo Router.
func (a *App) Handler() http.Handler {
	a.prepareRouter(a.router)
	return middleware.Recovery(slog.Default())(a.router.Handler(a.middleware))
}

// prepareRouter injeta o container DI e o tratador de erros em todas as rotas,
// incluindo as de sub-routers criados via Group, e recompila os handlers.
func (a *App) prepareRouter(r *Router) {
	r.mu.Lock()
	for _, route := range r.allRoutes {
		a.prepareRoute(route)
	}
	subs := append([]*Router(nil), r.subRouters...)
	r.mu.Unlock()

	for _, sub := range subs {
		a.prepareRouter(sub)
	}
}

func (a *App) prepareRoute(route *Route) {
	route.errorHandler = a.handleError
	route.di = a.di
	if route.Method != "MOUNT" {
		route.Plan = compileHandler(route.rawHandler, route)
		route.compiledHandler = compileRouteHTTPHandler(route)
	}
}

func (a *App) handleError(ctx *Context, err error) bool {
	a.mu.RLock()
	handlers := make(map[interface{}]ErrorHandler, len(a.exceptionHandlers))
	for key, handler := range a.exceptionHandlers {
		handlers[key] = handler
	}
	a.mu.RUnlock()
	for key, handler := range handlers {
		matched := false
		switch k := key.(type) {
		case reflect.Type:
			if k != nil && k.Implements(reflect.TypeOf((*error)(nil)).Elem()) {
				target := reflect.New(k).Interface()
				matched = errors.As(err, target)
			} else {
				matched = reflect.TypeOf(err) == k
			}
		case error:
			matched = errors.Is(err, k)
		case func(error) bool:
			matched = k(err)
		default:
			matched = reflect.TypeOf(err) == reflect.TypeOf(key)
		}
		if matched && handler != nil {
			return handler(ctx, err) == nil
		}
	}
	return false
}

// State retorna o estado compartilhado da aplicação.
func (a *App) State() *State { return a.state }

// OpenAPI gera o documento OpenAPI 3.1.
func (a *App) OpenAPI() *OpenAPIDocument {
	doc := NewOpenAPIDocument(a.title, a.version, a.description)
	doc.Servers = []Server{{URL: "/"}}
	doc.Paths = a.router.OpenAPIPaths()

	// Adicionar security schemes se existirem
	if a.openAPIDoc != nil && a.openAPIDoc.Components != nil && len(a.openAPIDoc.Components.SecuritySchemes) > 0 {
		doc.Components = &Components{
			Schemas:         make(map[string]any),
			SecuritySchemes: a.openAPIDoc.Components.SecuritySchemes,
		}
	}

	return doc
}

// Run inicia o servidor de produção (equivalente a uvicorn.run).
//
// Quando cfg.Workers > 1, inicia o Supervisor multi-processo que gerencia N
// processos filhos, cada um servindo no mesmo socket compartilhado.
// Quando a variável de ambiente GOFAST_WORKER=1 está definida, o processo
// é um worker filho e serve diretamente com o listener herdado via file
// descriptor (GOFAST_LISTENER_FD).
func (a *App) Run(cfg ServerConfig) error {
	a.stopOnce = sync.Once{}
	a.stopErr = nil
	a.serverConfig = &cfg

	// Configurar logging
	logger := slog.Default()
	if cfg.Logger != nil {
		logger = cfg.Logger
	} else {
		var level slog.Level
		switch cfg.LogLevel {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn", "warning":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}
		logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	}

	// Adicionar middlewares automáticos baseados na configuração
	autoMiddleware := []Middleware{}

	// Access log middleware
	if cfg.AccessLog {
		autoMiddleware = append(autoMiddleware, middleware.AccessLog(middleware.AccessLogConfig{
			Logger:    logger,
			Levels:    slog.LevelInfo,
			SkipPaths: []string{"/health/live", "/health/ready"},
		}))
	}

	// Proxy middleware (TrustedProxies, ForwardedHeaders, RootPath)
	if cfg.ProxyHeaders || cfg.RootPath != "" || len(cfg.TrustedProxies) > 0 {
		autoMiddleware = append(autoMiddleware, middleware.Proxy(middleware.ProxyConfig{
			ProxyHeaders:      cfg.ProxyHeaders,
			TrustedProxies:    cfg.TrustedProxies,
			ForwardedHeaders:  cfg.ForwardedHeaders,
			ForwardedAllowIPs: cfg.ForwardedAllowIPs,
			RootPath:          cfg.RootPath,
			BasePath:          cfg.BasePath,
		}))
	}

	// TrustedHost middleware
	if len(cfg.TrustedHosts) > 0 {
		autoMiddleware = append(autoMiddleware, middleware.TrustedHost(cfg.TrustedHosts))
	}

	// Limites de body
	if cfg.MaxBodyBytes > 0 {
		autoMiddleware = append(autoMiddleware, middleware.BodyLimit(cfg.MaxBodyBytes))
	}

	// Limite de concorrência
	if cfg.ConcurrencyLimit > 0 {
		autoMiddleware = append(autoMiddleware, middleware.ConcurrencyLimit(cfg.ConcurrencyLimit))
	}

	if cfg.ServerHeader != "" || len(cfg.DefaultHeaders) > 0 {
		autoMiddleware = append(autoMiddleware, func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if cfg.ServerHeader != "" {
					w.Header().Set("Server", cfg.ServerHeader)
				}
				for k, v := range cfg.DefaultHeaders {
					if w.Header().Get(k) == "" {
						w.Header().Set(k, v)
					}
				}
				next.ServeHTTP(w, r)
			})
		})
	}

	if cfg.Reload && cfg.Workers > 1 {
		return fmt.Errorf("Reload and Workers > 1 are mutually exclusive")
	}

	// Inicializar hot reloader se solicitado
	if cfg.Reload {
		reloadDirs := cfg.ReloadDirs
		if len(reloadDirs) == 0 {
			reloadDirs = []string{"."}
		}
		reloadDelay := cfg.ReloadDelay
		if reloadDelay == 0 {
			reloadDelay = 1 * time.Second
		}
		reloader := hotreload.NewHotReloader(a, reloadDirs, reloadDelay)
		a.hotReloader = reloader
		if err := reloader.Start(); err != nil {
			return fmt.Errorf("failed to start hot reloader: %w", err)
		}
	}
	if cfg.HTTP2 && cfg.H2C {
		return fmt.Errorf("HTTP/2 (TLS) and h2c (cleartext) are mutually exclusive")
	}
	if cfg.HTTP2 && (cfg.CertFile == "" || cfg.KeyFile == "") {
		return fmt.Errorf("HTTP/2 requires TLS (CertFile and KeyFile must be set)")
	}
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.Workers > 1 && cfg.WorkerMode != "" && cfg.WorkerMode != "process" {
		return fmt.Errorf("unsupported WorkerMode %q: only process workers are implemented", cfg.WorkerMode)
	}

	// Construir o handler com todos os middlewares (apenas uma vez)
	originalHandler := a.Handler()
	wrappedHandler := originalHandler
	for i := len(autoMiddleware) - 1; i >= 0; i-- {
		wrappedHandler = autoMiddleware[i](wrappedHandler)
	}

	// Worker filho: cada processo executa seus próprios hooks de startup.
	if os.Getenv("GOFAST_WORKER") == "1" {
		if err := a.runStartHooks(); err != nil {
			return err
		}
		a.healthManager.SetReady(true)
		logger.Info("gofastapi worker mode", "worker", os.Getenv("GOFAST_WORKER_ID"))
		return a.serveWorker(cfg, wrappedHandler, logger)
	}

	// O processo pai não inicializa dependências de aplicação; os workers fazem isso.
	if cfg.Workers > 1 {
		return a.runWithSupervisor(cfg, wrappedHandler, logger)
	}

	if err := a.runStartHooks(); err != nil {
		return err
	}
	a.healthManager.SetReady(true)

	// Se hot reload está ativo, monitorar mudanças e reiniciar o processo.
	// Run permanece bloqueado enquanto o servidor estiver em execução.
	if cfg.Reload && a.hotReloader != nil {
		return a.runWithHotReload(cfg, wrappedHandler, logger, a.hotReloader)
	}

	return a.runSingle(cfg, wrappedHandler, logger)
}

// runWithHotReload inicia o servidor e monitora mudanças de arquivos.
// Quando uma mudança é detectada, o código é recompilado com `go build` e um
// novo processo assume o servidor; falhas de compilação mantêm o servidor atual.
func (a *App) runWithHotReload(cfg ServerConfig, handler http.Handler, logger *slog.Logger, reloader *hotreload.HotReloader) error {
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- a.runSingle(cfg, handler, logger)
	}()

	for {
		select {
		case <-reloader.RestartRequested():
			logger.Info("GoFastAPI hot reload: mudança detectada, recompilando...")
			exe, err := hotreload.Build(cfg.ReloadPackage)
			if err != nil {
				logger.Error("GoFastAPI hot reload: falha na compilação; mantendo servidor atual", "error", err)
				// Continua servindo; a próxima mudança tenta novamente.
				continue
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.GracefulTimeout)
			if err := a.Shutdown(shutdownCtx); err != nil {
				logger.Warn("GoFastAPI hot reload: erro durante shutdown", "error", err)
			}
			cancel()
			logger.Info("GoFastAPI hot reload: reiniciando processo com o novo binário")
			return hotreload.Restart(exe, os.Args[1:])
		case err := <-serverErr:
			if err != nil && err != http.ErrServerClosed {
				logger.Error("GoFastAPI server error", "error", err)
			}
			return err
		}
	}
}

// runSingle inicia um único processo servidor (modo padrão, Workers == 1).
func (a *App) runSingle(cfg ServerConfig, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Handler: handler, ReadTimeout: cfg.ReadTimeout, ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout: cfg.WriteTimeout, IdleTimeout: cfg.IdleTimeout, MaxHeaderBytes: cfg.MaxHeaderBytes,
	}
	h2Handler, err := configureHTTP2(srv, handler, cfg)
	if err != nil {
		return fmt.Errorf("failed to configure HTTP/2 server: %w", err)
	}
	srv.Handler = h2Handler
	srv.SetKeepAlivesEnabled(!cfg.DisableKeepAlives)
	if cfg.MaxConnections > 0 {
		var active int64
		srv.ConnState = func(conn net.Conn, state http.ConnState) {
			switch state {
			case http.StateNew:
				if atomic.AddInt64(&active, 1) > int64(cfg.MaxConnections) {
					_ = conn.Close()
				}
			case http.StateClosed, http.StateHijacked:
				atomic.AddInt64(&active, -1)
			}
		}
	}
	ln, err := a.createListener(cfg)
	if err != nil {
		return err
	}
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		ln, err = a.wrapTLS(ln, cfg)
		if err != nil {
			_ = ln.Close()
			return err
		}
	}
	a.serverMu.Lock()
	a.server = srv
	a.serverMu.Unlock()
	if cfg.HandleSignals {
		go a.setupSignalHandler(srv, cfg, logger)
	}
	logger.Info("GoFastAPI server starting", "addr", ln.Addr().String(), "workers", 1)
	err = srv.Serve(ln) // Run bloqueia até Shutdown ou erro fatal.
	if err == http.ErrServerClosed {
		err = nil
	}
	a.healthManager.SetReady(false)
	stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownHooksTimeout)
	defer cancel()
	if stopErr := a.runStopHooks(stopCtx); err == nil {
		err = stopErr
	}
	a.serverMu.Lock()
	a.server = nil
	a.serverMu.Unlock()
	return err
}

// configureHTTP2 habilita HTTP/2 sobre TLS ou h2c (cleartext) no servidor.
// Para h2c, o handler precisa ser envolvido por h2c.NewHandler para tratar
// upgrade e prior knowledge; para HTTP/2 sobre TLS, basta configurar o servidor
// e anunciar "h2" no ALPN do listener (ver wrapTLS).
func configureHTTP2(srv *http.Server, handler http.Handler, cfg ServerConfig) (http.Handler, error) {
	if cfg.H2C {
		return h2c.NewHandler(handler, &http2.Server{}), nil
	}
	if cfg.HTTP2 {
		if err := http2.ConfigureServer(srv, &http2.Server{}); err != nil {
			return nil, err
		}
	}
	return handler, nil
}

func (a *App) runStartHooks() error {
	a.mu.RLock()
	hooks := append([]LifecycleHook(nil), a.onStart...)
	a.mu.RUnlock()
	for _, hook := range hooks {
		if err := hook(context.Background()); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) runStopHooks(ctx context.Context) error {
	a.stopOnce.Do(func() {
		a.healthManager.SetReady(false)
		a.mu.RLock()
		hooks := append([]LifecycleHook(nil), a.onStop...)
		a.mu.RUnlock()
		for i := len(hooks) - 1; i >= 0; i-- {
			if err := hooks[i](ctx); err != nil && a.stopErr == nil {
				a.stopErr = err
			}
		}
		if a.di != nil {
			if err := a.di.Close(); err != nil && a.stopErr == nil {
				a.stopErr = err
			}
		}
	})
	return a.stopErr
}

// runWithSupervisor inicia o Supervisor multi-processo quando Workers > 1.
// O supervisor abre o listener compartilhado e inicia N processos filhos
// (workers) que servem no mesmo socket.
func (a *App) runWithSupervisor(cfg ServerConfig, handler http.Handler, logger *slog.Logger) error {
	_ = handler // o handler é servido por cada worker via serveWorker

	// Criar listener compartilhado (sem TLS; cada worker termina TLS
	// independentemente sobre o listener herdado via FD)
	ln, err := a.createListener(cfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		logger.Info("TLS configurado: cada worker encerrará TLS independentemente sobre o listener herdado")
	}

	// Configurar o Supervisor com base em ServerConfig
	supCfg := SupervisorConfig{
		Workers:                 cfg.Workers,
		WorkerMode:              cfg.WorkerMode,
		WorkerStartTimeout:      cfg.WorkerStartTimeout,
		WorkerHealthTimeout:     cfg.WorkerHealthTimeout,
		RestartPolicy:           cfg.RestartPolicy,
		MaxWorkerRestarts:       cfg.MaxWorkerRestarts,
		MaxRequests:             cfg.MaxRequests,
		MaxRequestsJitter:       cfg.MaxRequestsJitter,
		PIDFile:                 cfg.PIDFile,
		SystemdNotify:           cfg.SystemdNotify,
		SystemdSocketActivation: cfg.SystemdSocketActivation,
		HandleSignals:           cfg.HandleSignals,
		ShutdownTimeout:         cfg.GracefulTimeout,
		Logger:                  logger,
	}

	sup := NewSupervisor(supCfg)

	// Iniciar supervisor (ele inicia N workers filhos no mesmo listener)
	appPath := os.Args[0]
	if err := sup.Start(appPath, os.Args[1:], ln); err != nil {
		return fmt.Errorf("failed to start supervisor: %w", err)
	}

	logger.Info("GoFastAPI supervisor started", "workers", cfg.Workers, "addr", ln.Addr().String())

	// Aguardar o supervisor ser parado (bloqueia o processo pai)
	sup.Wait()

	return nil
}

// serveWorker é executado pelo processo filho worker. Recupera o listener
// compartilhado via file descriptor (GOFAST_LISTENER_FD) e serve requisições.
func (a *App) serveWorker(cfg ServerConfig, handler http.Handler, logger *slog.Logger) error {
	fd := 3
	if v := os.Getenv("GOFAST_LISTENER_FD"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &fd); err != nil || n != 1 {
			fd = 3
		}
	}

	f := os.NewFile(uintptr(fd), "listener")
	if f == nil {
		return fmt.Errorf("gofastapi worker: cannot open listener file descriptor %d", fd)
	}
	defer f.Close()

	ln, err := net.FileListener(f)
	if err != nil {
		return fmt.Errorf("gofastapi worker: cannot create listener from fd %d: %w", fd, err)
	}

	// Configurar TLS se fornecido (cada worker termina TLS independentemente)
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		ln, err = a.wrapTLS(ln, cfg)
		if err != nil {
			return fmt.Errorf("gofastapi worker: cannot wrap listener with TLS: %w", err)
		}
	}

	maxRequests := cfg.MaxRequests
	if value := os.Getenv("GOFAST_MAX_REQUESTS"); value != "" {
		if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed > 0 {
			maxRequests = parsed
		}
	}
	if jitterValue := os.Getenv("GOFAST_MAX_REQUESTS_JITTER"); jitterValue != "" {
		if jitter, parseErr := strconv.Atoi(jitterValue); parseErr == nil && jitter > 0 {
			maxRequests += mathrand.Intn(jitter + 1)
		}
	}
	var requestCount atomic.Int64
	var srv *http.Server
	workerHandler := handler
	if maxRequests > 0 {
		workerHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handler.ServeHTTP(w, r)
			if requestCount.Add(1) >= int64(maxRequests) {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.GracefulTimeout)
					defer cancel()
					_ = srv.Shutdown(ctx)
				}()
			}
		})
	}
	srv = &http.Server{
		Handler:           workerHandler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
	h2Handler, h2Err := configureHTTP2(srv, workerHandler, cfg)
	if h2Err != nil {
		return fmt.Errorf("gofastapi worker: cannot configure HTTP/2: %w", h2Err)
	}
	srv.Handler = h2Handler
	srv.SetKeepAlivesEnabled(!cfg.DisableKeepAlives)

	done := make(chan error, 1)
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	go func() {
		logger.Info("gofastapi worker serving",
			"worker", os.Getenv("GOFAST_WORKER_ID"),
			"addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			done <- err
		}
		closeDone()
	}()
	if readyFD := os.Getenv("GOFAST_READY_FD"); readyFD != "" {
		if fd, parseErr := strconv.Atoi(readyFD); parseErr == nil && fd >= 0 {
			if readyFile := os.NewFile(uintptr(fd), "gofast-worker-ready"); readyFile != nil {
				_, _ = readyFile.Write([]byte("READY\n"))
				_ = readyFile.Close()
			}
		}
	}

	// Sinal de shutdown para graceful stop do worker
	if cfg.HandleSignals {
		go a.setupWorkerSignalHandler(srv, cfg, logger, done, closeDone)
	}

	// Bloquear até o servidor ser desligado
	if err := <-done; err != nil {
		return err
	}
	if maxRequests > 0 && requestCount.Load() >= int64(maxRequests) {
		return fmt.Errorf("worker reached MaxRequests recycle threshold (%d)", maxRequests)
	}
	return nil
}

// setupWorkerSignalHandler configura graceful shutdown para um worker filho.
func (a *App) setupWorkerSignalHandler(srv *http.Server, cfg ServerConfig, logger *slog.Logger, done chan error, closeDone func()) {
	sigChan := make(chan os.Signal, 1)

	signals := cfg.ShutdownSignals
	if len(signals) == 0 {
		signals = []string{"SIGINT", "SIGTERM"}
	}

	for _, sigName := range signals {
		var sig os.Signal
		switch sigName {
		case "SIGINT":
			sig = syscall.SIGINT
		case "SIGTERM":
			sig = syscall.SIGTERM
		case "SIGHUP":
			sig = syscall.SIGHUP
		case "SIGQUIT":
			sig = syscall.SIGQUIT
		}
		if sig != nil {
			signal.Notify(sigChan, sig)
		}
	}

	go func() {
		sig := <-sigChan
		logger.Info("gofastapi worker received signal", "signal", sig.String(), "worker", os.Getenv("GOFAST_WORKER_ID"))

		ctx, cancel := context.WithTimeout(context.Background(), cfg.GracefulTimeout)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("gofastapi worker shutdown error", "error", err)
			done <- err
		}

		// Executar hooks de shutdown do app
		appCtx, appCancel := context.WithTimeout(context.Background(), cfg.ShutdownHooksTimeout)
		defer appCancel()
		if err := a.Shutdown(appCtx); err != nil {
			logger.Warn("gofastapi worker app shutdown hooks error", "error", err)
		}

		closeDone()
	}()
}

// createListener cria o listener de rede com base na ServerConfig.
func (a *App) createListener(cfg ServerConfig) (net.Listener, error) {
	if cfg.SystemdSocketActivation && os.Getenv("GOFAST_WORKER") != "1" {
		if pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID")); pid != os.Getpid() {
			return nil, fmt.Errorf("systemd socket activation LISTEN_PID does not match process")
		}
		count, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
		if count < 1 {
			return nil, fmt.Errorf("systemd socket activation requested but LISTEN_FDS is empty")
		}
		file := os.NewFile(3, "systemd-listener")
		if file == nil {
			return nil, fmt.Errorf("systemd listener fd 3 is unavailable")
		}
		defer file.Close()
		listener, err := net.FileListener(file)
		if err != nil {
			return nil, fmt.Errorf("adopt systemd listener fd 3: %w", err)
		}
		return listener, nil
	}
	// File descriptor herdado é explícito para permitir fd 0 quando necessário.
	if cfg.UseFD {
		f := os.NewFile(cfg.FD, "gofast-listener")
		if f == nil {
			return nil, fmt.Errorf("invalid listener file descriptor %d", cfg.FD)
		}
		defer f.Close()
		ln, err := net.FileListener(f)
		if err != nil {
			return nil, fmt.Errorf("failed to adopt listener fd %d: %w", cfg.FD, err)
		}
		return ln, nil
	}
	// Determinar endereço de escuta
	if cfg.UDS != "" {
		ln, err := net.Listen("unix", cfg.UDS)
		if err != nil {
			return nil, fmt.Errorf("failed to listen on UDS %s: %w", cfg.UDS, err)
		}
		return ln, nil
	} else if cfg.Addr != "" {
		ln, err := net.Listen("tcp", cfg.Addr)
		if err != nil {
			return nil, fmt.Errorf("failed to listen on %s: %w", cfg.Addr, err)
		}
		return ln, nil
	} else if cfg.Host != "" && cfg.Port > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
		if err != nil {
			return nil, fmt.Errorf("failed to listen on %s:%d: %w", cfg.Host, cfg.Port, err)
		}
		return ln, nil
	}
	return nil, fmt.Errorf("no listen address configured")
}

// wrapTLS envolve um listener com TLS com base na ServerConfig.
func (a *App) wrapTLS(ln net.Listener, cfg ServerConfig) (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if cfg.HTTP2 {
		tlsConfig.NextProtos = []string{"h2", "http/1.1"}
	}

	if cfg.MinTLSVersion != "" {
		switch cfg.MinTLSVersion {
		case "1.0":
			tlsConfig.MinVersion = tls.VersionTLS10
		case "1.1":
			tlsConfig.MinVersion = tls.VersionTLS11
		case "1.2":
			tlsConfig.MinVersion = tls.VersionTLS12
		case "1.3":
			tlsConfig.MinVersion = tls.VersionTLS13
		}
	}

	if cfg.MaxTLSVersion != "" {
		switch cfg.MaxTLSVersion {
		case "1.2":
			tlsConfig.MaxVersion = tls.VersionTLS12
		case "1.3":
			tlsConfig.MaxVersion = tls.VersionTLS13
		}
	}

	if len(cfg.TLSCipherSuites) > 0 {
		var cipherSuites []uint16
		for _, cs := range cfg.TLSCipherSuites {
			switch cs {
			case "TLS_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_AES_128_GCM_SHA256)
			case "TLS_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_AES_256_GCM_SHA384)
			case "TLS_CHACHA20_POLY1305_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_CHACHA20_POLY1305_SHA256)
			case "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256)
			case "TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384)
			case "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384":
				cipherSuites = append(cipherSuites, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384)
			}
		}
		if len(cipherSuites) > 0 {
			tlsConfig.CipherSuites = cipherSuites
			if cfg.PreferServerCipherSuites {
				tlsConfig.PreferServerCipherSuites = true
			}
		}
	}

	if cfg.ClientCAFile != "" {
		clientCA, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client CA: %w", err)
		}
		clientCertPool := x509.NewCertPool()
		clientCertPool.AppendCertsFromPEM(clientCA)
		tlsConfig.ClientCAs = clientCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tls.NewListener(ln, tlsConfig), nil
}

// setupSignalHandler configura handlers para sinais do sistema (SIGINT, SIGTERM).
func (a *App) setupSignalHandler(srv *http.Server, cfg ServerConfig, logger *slog.Logger) {
	sigChan := make(chan os.Signal, 1)

	signals := cfg.ShutdownSignals
	if len(signals) == 0 {
		signals = []string{"SIGINT", "SIGTERM"}
	}

	for _, sigName := range signals {
		var sig os.Signal
		switch sigName {
		case "SIGINT":
			sig = syscall.SIGINT
		case "SIGTERM":
			sig = syscall.SIGTERM
		case "SIGHUP":
			sig = syscall.SIGHUP
		case "SIGQUIT":
			sig = syscall.SIGQUIT
		}
		if sig != nil {
			signal.Notify(sigChan, sig)
		}
	}

	go func() {
		sig := <-sigChan
		logger.Info("GoFastAPI received signal", "signal", sig.String())

		ctx, cancel := context.WithTimeout(context.Background(), cfg.GracefulTimeout)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("GoFastAPI shutdown error", "error", err)
		}

		// Executar hooks de shutdown do app
		appCtx, appCancel := context.WithTimeout(context.Background(), cfg.ShutdownHooksTimeout)
		defer appCancel()
		if err := a.runStopHooks(appCtx); err != nil {
			logger.Warn("GoFastAPI app shutdown hooks error", "error", err)
		}
	}()
}

// Serve atende requisições em um listener customizado. A chamada bloqueia até
// o listener fechar ou o contexto ser cancelado.
func (a *App) Serve(ctx context.Context, l net.Listener) error {
	a.stopOnce = sync.Once{}
	a.stopErr = nil
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.runStartHooks(); err != nil {
		return err
	}
	a.healthManager.SetReady(true)
	srv := &http.Server{Handler: a.Handler()}
	a.serverMu.Lock()
	a.server = srv
	a.serverMu.Unlock()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
	}()
	err := srv.Serve(l)
	if err == http.ErrServerClosed {
		err = nil
	}
	if stopErr := a.runStopHooks(context.Background()); err == nil {
		err = stopErr
	}
	a.serverMu.Lock()
	a.server = nil
	a.serverMu.Unlock()
	return err
}

// Shutdown interrompe a aceitação de novas conexões e executa hooks de parada.
func (a *App) Shutdown(ctx context.Context) error {
	a.serverMu.Lock()
	srv := a.server
	a.serverMu.Unlock()
	var shutdownErr error
	if srv != nil {
		shutdownErr = srv.Shutdown(ctx)
	}
	stopErr := a.runStopHooks(ctx)
	if shutdownErr != nil {
		return shutdownErr
	}
	return stopErr
}

// RouteOption configura uma rota.
type RouteOption struct {
	StatusCode      int
	Tags            []string
	Summary         string
	Description     string
	OperationID     string
	Responses       map[string]any
	Deprecated      bool
	IncludeInSchema bool
	ExcludeInSchema bool
	ResponseModel   any
	Dependencies    []Depends
	Security        []SecurityRequirement
}

// IncludeOption configura a inclusão de um router.
type IncludeOption struct {
	Prefix       string
	Tags         []string
	Dependencies []Depends
}

// ProvideOption configura um provider DI.
type ProvideOption struct {
	Scope Scope
}

// Scope define o ciclo de vida de uma dependência DI.
//
// ScopeUnset (0) é o valor zero e indica "não definido"; Provide() usa o
// padrão (request) quando nenhuma opção de escopo é informada. Isso evita o
// bug em que ScopeSingleton (antes 0) era ignorado pela verificação
// `opt.Scope != 0` em Provide().
type Scope int

const (
	ScopeUnset     Scope = iota // 0 — não definido, usa default (request)
	ScopeSingleton              // 1 — instância por processo (sync.Once)
	ScopeRequest                // 2 — instância por requisição (ctx.state)
	ScopeTransient              // 3 — nova instância a cada resolução
)

// Middleware é o tipo de middleware do GoFastAPI.
type Middleware func(http.Handler) http.Handler

// ErrorHandler trata erros de exceção.
type ErrorHandler func(*Context, error) error

// LifecycleHook é um hook de ciclo de vida (startup/shutdown).
type LifecycleHook func(context.Context) error

// Depends marca um parâmetro como dependência DI.
type Depends struct {
	Dependency any
	UseCache   bool
}

// Singleton retorna uma ProvideOption com escopo singleton.
func Singleton() ProvideOption {
	return ProvideOption{Scope: ScopeSingleton}
}

// Request retorna uma ProvideOption com escopo de requisição.
func Request() ProvideOption {
	return ProvideOption{Scope: ScopeRequest}
}

// Transient retorna uma ProvideOption com escopo transitório.
func Transient() ProvideOption {
	return ProvideOption{Scope: ScopeTransient}
}

// State é o estado da aplicação.
type State struct {
	mu   sync.RWMutex
	data map[string]any
}

// NewState cria um novo estado.
func NewState() *State {
	return &State{data: make(map[string]any)}
}

// Get obtém um valor do estado.
func (s *State) Get(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

// Set define um valor no estado.
func (s *State) Set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// RateLimit cria um limitador por IP com bucket de tokens. O formato aceita
// "30/second", "100/minute", "1000/hour" ou "10000/day". O bucket repõe
// tokens continuamente durante a janela; o estado é privado desta instância
// de middleware e entradas ociosas são removidas periodicamente.
func RateLimit(rate string) Middleware {
	limit, window, err := parseRateLimit(rate)
	if err != nil {
		slog.Warn("GoFastAPI: invalid rate limit; using 60/minute", "rate", rate, "error", err)
		limit, window = 60, time.Minute
	}
	type bucket struct {
		tokens    float64
		lastSeen  time.Time
		lastAdded time.Time
	}
	var mu sync.Mutex
	clients := make(map[string]*bucket)
	nextCleanup := time.Now().Add(time.Minute)
	refillPerSecond := float64(limit) / window.Seconds()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := rateLimitClientKey(r)
			now := time.Now()
			mu.Lock()
			if now.After(nextCleanup) {
				// Remove buckets ociosos por pelo menos duas janelas. Isso mantém
				// o mapa limitado em aplicações com muitos IPs efêmeros.
				idleTTL := 2 * window
				if idleTTL < time.Minute {
					idleTTL = time.Minute
				}
				for client, b := range clients {
					if now.Sub(b.lastSeen) > idleTTL {
						delete(clients, client)
					}
				}
				nextCleanup = now.Add(time.Minute)
			}

			b := clients[key]
			if b == nil {
				b = &bucket{tokens: float64(limit), lastSeen: now, lastAdded: now}
				clients[key] = b
			}
			elapsed := now.Sub(b.lastAdded).Seconds()
			if elapsed > 0 {
				b.tokens += elapsed * refillPerSecond
				if b.tokens > float64(limit) {
					b.tokens = float64(limit)
				}
				b.lastAdded = now
			}
			b.lastSeen = now
			allowed := b.tokens >= 1
			retryAfter := 1
			if allowed {
				b.tokens--
			} else if refillPerSecond > 0 {
				retryAfter = int(math.Ceil((1 - b.tokens) / refillPerSecond))
				if retryAfter < 1 {
					retryAfter = 1
				}
			}
			mu.Unlock()

			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func parseRateLimit(value string) (int, time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(value)), "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected <count>/<period>")
	}
	count, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || count <= 0 {
		return 0, 0, fmt.Errorf("count must be a positive integer")
	}
	var window time.Duration
	switch strings.TrimSpace(parts[1]) {
	case "s", "sec", "second", "seconds":
		window = time.Second
	case "m", "min", "minute", "minutes":
		window = time.Minute
	case "h", "hr", "hour", "hours":
		window = time.Hour
	case "d", "day", "days":
		window = 24 * time.Hour
	case "w", "week", "weeks":
		window = 7 * 24 * time.Hour
	default:
		return 0, 0, fmt.Errorf("unsupported period %q", parts[1])
	}
	return count, window, nil
}

func rateLimitClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

// NewOpenAPIDocument cria um novo documento OpenAPI.
func NewOpenAPIDocument(title, version, description string) *OpenAPIDocument {
	return &OpenAPIDocument{
		OpenAPI: "3.1.0",
		Info: Info{
			Title:       title,
			Version:     version,
			Description: description,
		},
		Paths: make(map[string]any),
		Components: &Components{
			Schemas:         make(map[string]any),
			SecuritySchemes: make(map[string]SecurityScheme),
		},
	}
}

// swaggerUIHTML gera o HTML do Swagger UI.
func swaggerUIHTML(title, openapiURL string) string {
	return `<!DOCTYPE html><html><head><title>` + title + ` - Swagger UI</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>const ui = SwaggerUIBundle({url: "` + openapiURL + `", dom_id: '#swagger-ui', presets: [SwaggerUIBundle.presets.apis], layout: "StandaloneLayout"})</script></body></html>`
}

// redocHTML gera o HTML do ReDoc.
func redocHTML(title, openapiURL string) string {
	return `<!DOCTYPE html><html><head><title>` + title + ` - ReDoc</title><style>body {margin: 0;padding: 0;}redoc {display: block;height: 100vh;}</style></head><body><redoc spec-url="` + openapiURL + `"></redoc><script src="https://unpkg.com/redoc@next/bundles/redoc.standalone.js"></script></body></html>`
}
