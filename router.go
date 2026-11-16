package gofast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// HandlerPlan é o plano compilado de um handler, produzido no registro da rota.
// Handlers com assinatura HandlerFunc executam sem reflexão no hot path. As
// assinaturas tipadas genéricas (body/path/dependencies) usam reflect.Call para
// materializar argumentos e invocar a função em cada requisição.
type HandlerPlan struct {
	Method          string
	Path            string
	PathPattern     string
	PathParams      []string
	PathRegex       *regexp.Regexp
	Handler         HandlerFunc
	StatusCode      int
	Tags            []string
	Summary         string
	Description     string
	OperationID     string
	Responses       map[string]any
	Deprecated      bool
	IncludeInSchema bool
	ResponseModel   any
	Dependencies    []Depends
	Middleware      []Middleware
	Security        []SecurityRequirement
}

// HandlerFunc é a assinatura final de um handler executável no hot path.
type HandlerFunc func(*Context) (any, error)

// Route representa uma rota registrada (metadados + plano compilado).
type Route struct {
	Method          string
	Path            string
	PathPattern     string
	PathParams      []string
	PathRegex       *regexp.Regexp
	Plan            *HandlerPlan
	StatusCode      int
	Tags            []string
	Summary         string
	Description     string
	OperationID     string
	Responses       map[string]any
	Deprecated      bool
	IncludeInSchema bool
	ResponseModel   any
	Dependencies    []Depends
	Middleware      []Middleware
	Security        []SecurityRequirement
	rawHandler      any
	compiledHandler http.Handler // cadeia de middleware compilada no registro
	errorHandler    func(*Context, error) bool
	di              *DIContainer
	mountHandler    http.Handler // legado; mounts usam Router.mounts
}

// routeNode é um nó da árvore de roteamento segmentada (trie).
// Cada nível representa um segmento de caminho. Segmentos estáticos têm
// prioridade sobre parâmetros ({param}).
type routeNode struct {
	staticChildren map[string]*routeNode // segmento estático -> nó filho
	paramChild     *routeNode            // nó para segmento de parâmetro {param}
	catchAllChild  *routeNode            // nó para parâmetro {name:path}
	route          *Route                // rota terminal neste nó (nil se intermediário)
	paramName      string                // nome do parâmetro se este nó representar {param}
}

// routeTrie é uma árvore de busca de rotas dinâmicas por segmento de caminho.
// Busca O(M) onde M = número de segmentos do caminho, evitando busca linear
// com expressões regulares.
type routeTrie struct {
	root *routeNode
}

func newRouteTrie() *routeTrie {
	return &routeTrie{
		root: &routeNode{
			staticChildren: make(map[string]*routeNode),
		},
	}
}

// insert adiciona uma rota dinâmica ao trie.
func (t *routeTrie) insert(path string, paramNames []string, route *Route) {
	node := t.root
	parts := strings.Split(path, "/")
	paramIdx := 0

	for i, part := range parts {
		if i == 0 && part == "" {
			continue // pular segmento vazio da barra inicial
		}
		if part == "" {
			continue // ignorar barras duplas
		}

		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			paramName := part[1 : len(part)-1]
			converter := ""
			if idx := strings.Index(paramName, ":"); idx > 0 {
				converter = paramName[idx+1:]
				paramName = paramName[:idx]
			}
			if converter == "path" {
				if node.catchAllChild == nil {
					node.catchAllChild = &routeNode{staticChildren: make(map[string]*routeNode), paramName: paramName}
				}
				node = node.catchAllChild
				break
			}
			if node.paramChild == nil {
				node.paramChild = &routeNode{staticChildren: make(map[string]*routeNode), paramName: paramName}
			}
			node = node.paramChild
			paramIdx++
		} else {
			if node.staticChildren == nil {
				node.staticChildren = make(map[string]*routeNode)
			}
			child, ok := node.staticChildren[part]
			if !ok {
				child = &routeNode{
					staticChildren: make(map[string]*routeNode),
				}
				node.staticChildren[part] = child
			}
			node = child
		}
	}
	node.route = route
}

// find busca uma rota dinâmica no trie pelo caminho da requisição.
// Retorna a rota e os parâmetros capturados, ou nil se não encontrar.
func (t *routeTrie) find(path string) (*Route, map[string]string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}
	return findRouteNode(t.root, parts, 0, nil)
}

// findRouteNode prioriza segmentos estáticos, mas faz backtracking para o
// parâmetro dinâmico quando o ramo estático não leva a uma rota terminal.
func findRouteNode(node *routeNode, parts []string, index int, params map[string]string) (*Route, map[string]string) {
	if node == nil {
		return nil, nil
	}
	if index >= len(parts) {
		if node.route != nil {
			return node.route, params
		}
		return nil, nil
	}
	segment := parts[index]
	if child := node.staticChildren[segment]; child != nil {
		if route, found := findRouteNode(child, parts, index+1, clonePathParams(params)); route != nil {
			return route, found
		}
	}
	if child := node.paramChild; child != nil {
		next := clonePathParams(params)
		if child.paramName != "" {
			next[child.paramName] = segment
		}
		if route, found := findRouteNode(child, parts, index+1, next); route != nil {
			return route, found
		}
	}
	if child := node.catchAllChild; child != nil {
		next := clonePathParams(params)
		if child.paramName != "" {
			next[child.paramName] = strings.Join(parts[index:], "/")
		}
		if child.route != nil {
			return child.route, next
		}
	}
	return nil, nil
}

func clonePathParams(params map[string]string) map[string]string {
	clone := make(map[string]string, len(params)+1)
	for key, value := range params {
		clone[key] = value
	}
	return clone
}

// Router gerencia rotas usando indexação por método para busca rápida.
// Rotas estáticas: mapa O(1). Rotas dinâmicas: árvore de segmentos O(M).
type mountedHandler struct {
	prefix  string
	handler http.Handler
}

type routeParamsContextKey struct{}

func routeKey(method, path string) string { return method + "\x00" + path }

type Router struct {
	routesByMethod map[string][]*Route
	staticRoutes   map[string]*Route
	routeTries     map[string]*routeTrie // trie de rotas dinâmicas por método
	mounts         []mountedHandler
	allRoutes      []*Route
	middleware     []Middleware
	prefix         string
	groupTags      []string
	groupDeps      []Depends
	mu             sync.RWMutex
}

// NewRouter cria um router vazio.
func NewRouter() *Router {
	return &Router{
		routesByMethod: make(map[string][]*Route),
		staticRoutes:   make(map[string]*Route),
		routeTries:     make(map[string]*routeTrie),
	}
}

// Use registra middlewares aplicados a todas as rotas deste router/grupo.
func (r *Router) Use(mw ...Middleware) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, mw...)
}

// Group cria um sub-router com prefixo e herda os middlewares já registrados.
type GroupOption struct {
	Tags         []string
	Dependencies []Depends
}

func GroupTags(tags ...string) GroupOption { return GroupOption{Tags: tags} }
func GroupDependencies(dependencies ...Depends) GroupOption {
	return GroupOption{Dependencies: dependencies}
}

func (r *Router) Group(prefix string, opts ...GroupOption) *Router {
	r.mu.RLock()
	inherited := append([]Middleware(nil), r.middleware...)
	parentPrefix := r.prefix
	inheritedTags := append([]string(nil), r.groupTags...)
	inheritedDeps := append([]Depends(nil), r.groupDeps...)
	r.mu.RUnlock()
	joined := strings.TrimSuffix(parentPrefix, "/") + "/" + strings.Trim(prefix, "/")
	if joined == "/" {
		joined = ""
	}
	g := NewRouter()
	g.prefix = joined
	g.middleware = inherited
	g.groupTags = inheritedTags
	g.groupDeps = inheritedDeps
	for _, opt := range opts {
		g.groupTags = append(g.groupTags, opt.Tags...)
		g.groupDeps = append(g.groupDeps, opt.Dependencies...)
	}
	return g
}

func (r *Router) GET(path string, handler any, opts ...any) {
	r.AddRoute(http.MethodGet, path, handler, opts...)
}
func (r *Router) POST(path string, handler any, opts ...any) {
	r.AddRoute(http.MethodPost, path, handler, opts...)
}
func (r *Router) PUT(path string, handler any, opts ...any) {
	r.AddRoute(http.MethodPut, path, handler, opts...)
}
func (r *Router) DELETE(path string, handler any, opts ...any) {
	r.AddRoute(http.MethodDelete, path, handler, opts...)
}

// AddRoute registra uma rota e compila seu HandlerPlan.
// Reflexão ocorre APENAS aqui (no startup), nunca no hot path.
func (r *Router) AddRoute(method, path string, handler any, opts ...any) {
	if r.prefix != "" {
		path = strings.TrimSuffix(r.prefix, "/") + "/" + strings.TrimPrefix(path, "/")
		if path == "" {
			path = "/"
		}
	}
	route := &Route{
		Method:          method,
		Path:            path,
		PathPattern:     path,
		StatusCode:      200,
		IncludeInSchema: true,
		Tags:            append([]string(nil), r.groupTags...),
		Dependencies:    append([]Depends(nil), r.groupDeps...),
	}

	// Extrair nomes dos parâmetros de caminho do padrão {param}
	paramNames := extractParamNames(path)
	route.PathParams = paramNames
	route.PathRegex = compilePathRegex(path)

	// Processar opções
	middlewares := append([]Middleware(nil), r.middleware...)
	for _, opt := range opts {
		switch v := opt.(type) {
		case RouteOption:
			if v.StatusCode != 0 {
				route.StatusCode = v.StatusCode
			}
			route.Tags = append(route.Tags, v.Tags...)
			route.Summary = v.Summary
			route.Description = v.Description
			route.OperationID = v.OperationID
			route.Responses = v.Responses
			route.Deprecated = v.Deprecated
			if v.IncludeInSchema {
				route.IncludeInSchema = true
			}
			if v.ExcludeInSchema {
				route.IncludeInSchema = false
			}
			route.ResponseModel = v.ResponseModel
			route.Dependencies = append(route.Dependencies, v.Dependencies...)
			route.Security = v.Security
		case Middleware:
			middlewares = append(middlewares, v)
		case func(http.Handler) http.Handler:
			middlewares = append(middlewares, v)
		}
	}
	route.Middleware = middlewares

	// Compilar o HandlerPlan e a cadeia de middlewares uma única vez.
	route.rawHandler = handler
	route.Plan = compileHandler(handler, route)
	route.compiledHandler = compileRouteHTTPHandler(route)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Indexar por método (mantido para OpenAPI e listagem)
	r.routesByMethod[method] = append(r.routesByMethod[method], route)

	// Indexar rotas estáticas (sem parâmetros) para busca O(1)
	if len(paramNames) == 0 {
		r.staticRoutes[routeKey(method, path)] = route
	} else {
		// Indexar rotas dinâmicas na árvore de segmentos para busca O(M)
		trie, ok := r.routeTries[method]
		if !ok {
			trie = newRouteTrie()
			r.routeTries[method] = trie
		}
		trie.insert(path, paramNames, route)
	}

	r.allRoutes = append(r.allRoutes, route)
}

// compileHandler inspeciona a assinatura do handler uma vez e produz um
// HandlerPlan com binders pré-compilados. Zero reflexão no hot path.
func compileHandler(handler any, route *Route) *HandlerPlan {
	plan := &HandlerPlan{
		Method:          route.Method,
		Path:            route.Path,
		PathPattern:     route.PathPattern,
		PathParams:      route.PathParams,
		StatusCode:      route.StatusCode,
		Tags:            route.Tags,
		Summary:         route.Summary,
		Description:     route.Description,
		Deprecated:      route.Deprecated,
		IncludeInSchema: route.IncludeInSchema,
		ResponseModel:   route.ResponseModel,
		Dependencies:    route.Dependencies,
		Middleware:      route.Middleware,
		Security:        route.Security,
	}

	// Validar a assinatura do handler e criar a função de execução
	plan.Handler = buildHandlerFunc(handler, route)

	return plan
}

// buildHandlerFunc cria uma HandlerFunc a partir de um handler any.
// Usa type assertion direta (idiomático em Go, sem reflection falso).
func buildHandlerFunc(handler any, route *Route) HandlerFunc {
	switch h := handler.(type) {
	case func(*Context) (any, error):
		return h
	case func(*Context, map[string]any) (any, error):
		return func(ctx *Context) (any, error) {
			var body map[string]any
			if err := ctx.JSON(&body); err != nil {
				body = make(map[string]any)
			}
			return h(ctx, body)
		}
	case func(*Context, []any) (any, error):
		return func(ctx *Context) (any, error) {
			var body []any
			if err := ctx.JSON(&body); err != nil {
				body = []any{}
			}
			return h(ctx, body)
		}
	case func(*Context, string) (any, error):
		name := "arg0"
		if len(route.PathParams) > 0 {
			name = route.PathParams[0]
		}
		return func(ctx *Context) (any, error) {
			val := ctx.Param(name)
			if val == "" {
				val = ctx.Query(name)
			}
			return h(ctx, val)
		}
	case func(*Context, int) (any, error):
		name := "arg0"
		if len(route.PathParams) > 0 {
			name = route.PathParams[0]
		}
		return func(ctx *Context) (any, error) {
			valStr := ctx.Param(name)
			if valStr == "" {
				valStr = ctx.Query(name)
			}
			val, err := strconv.Atoi(valStr)
			if err != nil {
				return nil, &ValidationError{Errors: []ValidationErrorDetail{{Location: []string{"path", name}, Field: name, Code: "type_error.integer", Message: "value must be an integer", Rejected: valStr}}}
			}
			return h(ctx, val)
		}
	case func(*Context, *string) (any, error):
		name := "arg0"
		if len(route.PathParams) > 0 {
			name = route.PathParams[0]
		}
		return func(ctx *Context) (any, error) {
			val := ctx.Param(name)
			if val == "" {
				val = ctx.Query(name)
			}
			if val == "" {
				return h(ctx, nil)
			}
			return h(ctx, &val)
		}
	case func(*Context, any) (any, error):
		return func(ctx *Context) (any, error) {
			var instance any
			if err := ctx.JSON(&instance); err != nil {
				return nil, fmt.Errorf("failed to bind body: %w", err)
			}
			return h(ctx, instance)
		}
	default:
		// Structs de entrada tipadas são decodificadas pelo encoding/json.
		// O caminho de execução usa reflect.Call apenas para assinaturas não
		// cobertas pelas formas comuns acima; não existe análise de tipos repetida.
		if typed := compileDynamicHandler(handler, route); typed != nil {
			return typed
		}
		return func(ctx *Context) (any, error) {
			return nil, fmt.Errorf("handler signature not supported: %T", handler)
		}
	}
}

// compileTypedStructHandler aceita func(*Context, T) (any, error) e
// func(*Context, *T) (any, error), quando T é struct. A assinatura é validada
// no startup; a decodificação JSON e chamada tipada ocorrem por requisição.
func compileTypedStructHandler(handler any) HandlerFunc {
	fn := reflect.ValueOf(handler)
	if !fn.IsValid() || fn.Kind() != reflect.Func {
		return nil
	}
	t := fn.Type()
	if t.NumIn() != 2 || t.In(0) != reflect.TypeOf((*Context)(nil)) || t.NumOut() != 2 {
		return nil
	}
	errType := reflect.TypeOf((*error)(nil)).Elem()
	if !t.Out(1).Implements(errType) {
		return nil
	}
	bodyType := t.In(1)
	pointerInput := bodyType.Kind() == reflect.Ptr
	valueType := bodyType
	if pointerInput {
		valueType = bodyType.Elem()
	}
	if valueType.Kind() != reflect.Struct {
		return nil
	}
	return func(ctx *Context) (any, error) {
		value := reflect.New(valueType)
		if err := ctx.JSON(value.Interface()); err != nil {
			return nil, fmt.Errorf("failed to bind request body: %w", err)
		}
		if validator, ok := value.Interface().(interface{ Validate() error }); ok {
			if err := validator.Validate(); err != nil {
				return nil, &ValidationError{Errors: []ValidationErrorDetail{{Location: []string{"body"}, Code: "value_error", Message: err.Error()}}}
			}
		}
		arg := value.Elem()
		if pointerInput {
			arg = value
		}
		results := fn.Call([]reflect.Value{reflect.ValueOf(ctx), arg})
		if err := reflectedError(results[1]); err != nil {
			return nil, err
		}
		if isNilable(results[0].Kind()) && results[0].IsNil() {
			return nil, nil
		}
		return results[0].Interface(), nil
	}
}

func dependencyReflectType(dependency any) reflect.Type {
	if dependency == nil {
		return nil
	}
	if typ, ok := dependency.(reflect.Type); ok {
		return typ
	}
	valueType := reflect.TypeOf(dependency)
	if valueType.Kind() == reflect.Func && valueType.NumOut() > 0 {
		return valueType.Out(0)
	}
	return valueType
}

func routeDeclaresDependency(route *Route, typ reflect.Type) bool {
	for _, dependency := range route.Dependencies {
		if dependencyReflectType(dependency.Dependency) == typ {
			return true
		}
	}
	return false
}

// routeHandlerConsumesDependency evita resolver duas vezes um provider Transient
// quando a mesma dependência está declarada na rota e é parâmetro do handler.
func routeHandlerConsumesDependency(route *Route, typ reflect.Type) bool {
	if route == nil || route.rawHandler == nil || typ == nil {
		return false
	}
	handlerType := reflect.TypeOf(route.rawHandler)
	if handlerType.Kind() != reflect.Func {
		return false
	}
	for i := 1; i < handlerType.NumIn(); i++ {
		if handlerType.In(i) == typ {
			return true
		}
	}
	return false
}

// compileDynamicHandler monta uma assinatura tipada com contexto, dependências
// explicitadas em RouteOption/GroupOption e structs como body JSON.
func compileDynamicHandler(handler any, route *Route) HandlerFunc {
	fn := reflect.ValueOf(handler)
	if !fn.IsValid() || fn.Kind() != reflect.Func {
		return nil
	}
	t := fn.Type()
	if t.NumIn() < 1 || t.In(0) != reflect.TypeOf((*Context)(nil)) || t.NumOut() < 1 || t.NumOut() > 2 {
		return nil
	}
	errType := reflect.TypeOf((*error)(nil)).Elem()
	if t.NumOut() == 2 && !t.Out(1).Implements(errType) {
		return nil
	}
	if t.NumOut() == 1 && t.Out(0).Implements(errType) {
		return nil
	}
	types := make([]reflect.Type, t.NumIn())
	for i := 0; i < t.NumIn(); i++ {
		types[i] = t.In(i)
	}
	return func(ctx *Context) (any, error) {
		args := make([]reflect.Value, len(types))
		args[0] = reflect.ValueOf(ctx)
		pathParamIndex := 0
		bodyDecoded := false
		for i := 1; i < len(types); i++ {
			paramType := types[i]
			if routeDeclaresDependency(route, paramType) {
				dependency, err := ctx.Resolve(paramType)
				if err != nil {
					return nil, err
				}
				value := reflect.ValueOf(dependency)
				if !value.IsValid() || !value.Type().AssignableTo(paramType) {
					return nil, fmt.Errorf("dependency %s is not assignable to handler parameter", paramType)
				}
				args[i] = value
				continue
			}
			baseType := paramType
			pointer := baseType.Kind() == reflect.Ptr
			if pointer {
				baseType = baseType.Elem()
			}
			if baseType.Kind() == reflect.Struct || baseType.Kind() == reflect.Map || baseType.Kind() == reflect.Slice || baseType.Kind() == reflect.Interface {
				if bodyDecoded {
					return nil, fmt.Errorf("only one JSON body parameter is supported")
				}
				value := reflect.New(baseType)
				if err := ctx.JSON(value.Interface()); err != nil {
					return nil, fmt.Errorf("failed to bind request body: %w", err)
				}
				if validator, ok := value.Interface().(interface{ Validate() error }); ok {
					if err := validator.Validate(); err != nil {
						return nil, &ValidationError{Errors: []ValidationErrorDetail{{Location: []string{"body"}, Code: "value_error", Message: err.Error()}}}
					}
				}
				bodyDecoded = true
				if pointer {
					args[i] = value
				} else {
					args[i] = value.Elem()
				}
				continue
			}
			if !isScalarKind(baseType.Kind()) {
				return nil, fmt.Errorf("unsupported handler parameter type %s", paramType)
			}
			name := fmt.Sprintf("arg%d", i-1)
			if pathParamIndex < len(route.PathParams) {
				name = route.PathParams[pathParamIndex]
				pathParamIndex++
			}
			value := ctx.Param(name)
			if value == "" {
				value = ctx.Query(name)
			}
			parsed, err := parseScalarValue(value, baseType)
			if err != nil {
				return nil, &ValidationError{Errors: []ValidationErrorDetail{{Location: []string{"path", name}, Field: name, Code: "type_error", Message: err.Error(), Rejected: value}}}
			}
			if pointer {
				ptr := reflect.New(baseType)
				ptr.Elem().Set(parsed)
				args[i] = ptr
			} else {
				args[i] = parsed
			}
		}

		results := fn.Call(args)
		if t.NumOut() == 2 {
			if err := reflectedError(results[1]); err != nil {
				return nil, err
			}
			if isNilable(results[0].Kind()) && results[0].IsNil() {
				return nil, nil
			}
			return results[0].Interface(), nil
		}
		if results[0].Type().Implements(errType) {
			if err := reflectedError(results[0]); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if isNilable(results[0].Kind()) && results[0].IsNil() {
			return nil, nil
		}
		return results[0].Interface(), nil
	}
}

func isScalarKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func parseScalarValue(raw string, typ reflect.Type) (reflect.Value, error) {
	value := reflect.New(typ).Elem()
	switch typ.Kind() {
	case reflect.String:
		value.SetString(raw)
	case reflect.Bool:
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetBool(parsed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(raw, 10, typ.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		parsed, err := strconv.ParseUint(raw, 10, typ.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		parsed, err := strconv.ParseFloat(raw, typ.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetFloat(parsed)
	default:
		return reflect.Value{}, fmt.Errorf("unsupported scalar kind %s", typ.Kind())
	}
	return value, nil
}

// Handler retorna o http.Handler para o router. A cadeia de middlewares de rota
// já foi compilada em AddRoute/Include; middlewares globais são aplicados uma vez.
func (r *Router) Handler(globalMiddleware []Middleware) http.Handler {
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		method, path := req.Method, req.URL.Path
		var selectedRoute *Route
		var selectedParams map[string]string
		var selectedMount *mountedHandler
		var allowed []string

		r.mu.RLock()
		if route := r.staticRoutes[routeKey(method, path)]; route != nil {
			selectedRoute = route
		}
		if selectedRoute == nil && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			selectedRoute = r.staticRoutes[routeKey("WEBSOCKET", path)]
		}
		if selectedRoute == nil {
			if trie := r.routeTries[method]; trie != nil {
				selectedRoute, selectedParams = trie.find(path)
			}
		}
		// net/http convention: GET routes also answer HEAD; the body is
		// suppressed while headers and status are preserved.
		if selectedRoute == nil && method == http.MethodHead {
			selectedRoute = r.staticRoutes[routeKey(http.MethodGet, path)]
			if selectedRoute == nil {
				if trie := r.routeTries[http.MethodGet]; trie != nil {
					selectedRoute, selectedParams = trie.find(path)
				}
			}
		}
		if selectedRoute == nil && strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
			if trie := r.routeTries["WEBSOCKET"]; trie != nil {
				selectedRoute, selectedParams = trie.find(path)
			}
		}

		// Mounts usam o prefixo mais específico e removem o prefixo antes de delegar.
		if selectedRoute == nil {
			for i := range r.mounts {
				m := &r.mounts[i]
				if (path == m.prefix || strings.HasPrefix(path, strings.TrimSuffix(m.prefix, "/")+"/")) &&
					(selectedMount == nil || len(m.prefix) > len(selectedMount.prefix)) {
					copy := *m
					selectedMount = &copy
				}
			}
		}

		// Diferenciar caminho inexistente (404) de método não permitido (405).
		if selectedRoute == nil && selectedMount == nil {
			for candidate := range r.routesByMethod {
				if candidate == method || candidate == "WEBSOCKET" || candidate == "MOUNT" {
					continue
				}
				matched := r.staticRoutes[routeKey(candidate, path)] != nil
				if !matched && r.routeTries[candidate] != nil {
					matchedRoute, _ := r.routeTries[candidate].find(path)
					matched = matchedRoute != nil
				}
				if matched {
					allowed = append(allowed, candidate)
				}
			}
		}
		r.mu.RUnlock()

		if selectedRoute != nil {
			if method == http.MethodHead && selectedRoute.Method == http.MethodGet {
				r.executeRoute(selectedRoute, selectedParams, headResponseWriter{ResponseWriter: w}, req)
			} else {
				r.executeRoute(selectedRoute, selectedParams, w, req)
			}
			return
		}
		if selectedMount != nil {
			clone := req.Clone(req.Context())
			u := *req.URL
			u.Path = strings.TrimPrefix(path, selectedMount.prefix)
			if u.Path == "" {
				u.Path = "/"
			}
			if !strings.HasPrefix(u.Path, "/") {
				u.Path = "/" + u.Path
			}
			clone.URL = &u
			selectedMount.handler.ServeHTTP(w, clone)
			return
		}
		if len(allowed) > 0 {
			if containsString(allowed, http.MethodGet) && !containsString(allowed, http.MethodHead) {
				allowed = append(allowed, http.MethodHead)
			}
			if !containsString(allowed, http.MethodOptions) {
				allowed = append(allowed, http.MethodOptions)
			}
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	})

	var h http.Handler = handler
	for i := len(globalMiddleware) - 1; i >= 0; i-- {
		h = globalMiddleware[i](h)
	}
	return h
}

type headResponseWriter struct{ http.ResponseWriter }

func (w headResponseWriter) Write(body []byte) (int, error) { return len(body), nil }

func compileRouteHTTPHandler(route *Route) http.Handler {
	base := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := NewContext(req.Context(), req, w)
		ctx.di = route.di
		if params, ok := req.Context().Value(routeParamsContextKey{}).(map[string]string); ok {
			ctx.params = params
		}
		defer func() {
			// Commit/flush somente quando existirem tarefas pós-resposta. Para
			// respostas comuns, preserva o buffering do net/http e Content-Length.
			if ctx.hasBackgroundTasks() {
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			ctx.runBackgroundTasks()
			ctx.runCleanups()
		}()

		if route.Method == "WEBSOCKET" {
			handler, ok := route.rawHandler.(WebSocketHandler)
			if !ok {
				if plain, ok2 := route.rawHandler.(func(*WebSocketConn) error); ok2 {
					handler = WebSocketHandler(plain)
					ok = true
				}
			}
			if !ok {
				http.Error(w, "unsupported websocket handler", http.StatusNotImplemented)
				return
			}
			conn, err := UpgradeWebSocket(w, req)
			if err != nil {
				http.Error(w, "websocket upgrade failed", http.StatusBadRequest)
				return
			}
			defer conn.Close()
			if err := handler(conn); err != nil {
				slog.Warn("GoFastAPI websocket handler failed", "error", err)
			}
			return
		}

		if route.Plan == nil || route.Plan.Handler == nil {
			http.Error(w, `{"error":"handler not implemented"}`, http.StatusNotImplemented)
			return
		}
		for _, dependency := range route.Dependencies {
			dependencyType := dependencyReflectType(dependency.Dependency)
			if dependencyType == nil || routeHandlerConsumesDependency(route, dependencyType) {
				continue
			}
			if _, depErr := ctx.Resolve(dependencyType); depErr != nil {
				renderRouteError(ctx, w, depErr)
				return
			}
		}
		result, err := route.Plan.Handler(ctx)
		if err != nil {
			if route.errorHandler != nil && route.errorHandler(ctx, err) {
				return
			}
			renderRouteError(ctx, w, err)
			return
		}
		if result == nil {
			if route.StatusCode != http.StatusOK {
				w.WriteHeader(route.StatusCode)
			}
			return
		}
		switch response := result.(type) {
		case Response:
			if err := response.Render(w); err != nil {
				slog.Warn("GoFastAPI response render failed", "error", err)
			}
		case SSEResponse:
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			if err := response.Render(w); err != nil {
				slog.Warn("GoFastAPI SSE failed", "error", err)
			}
		case StreamingResponse:
			contentType := response.contentType
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			if err := response.Render(w); err != nil {
				slog.Warn("GoFastAPI stream failed", "error", err)
			}
		default:
			jsonResp := NewJSONResponse(result, route.StatusCode)
			if err := jsonResp.Render(w); err != nil {
				slog.Warn("GoFastAPI JSON render failed", "error", err)
			}
		}
	})
	for i := len(route.Middleware) - 1; i >= 0; i-- {
		base = http.HandlerFunc(route.Middleware[i](base).ServeHTTP)
	}
	return base
}

func renderRouteError(ctx *Context, w http.ResponseWriter, err error) {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		for k, v := range httpErr.Headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(httpErr.Status)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "code": httpErr.Code, "message": httpErr.Message, "details": httpErr.Details})
		return
	}
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"detail": validationErr.Errors})
		return
	}
	// Não expor a causa interna ao cliente.
	slog.Error("GoFastAPI handler error", "error", err, "path", ctx.Path())
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "message": "internal server error"})
}

// executeRoute injeta os parâmetros do path e despacha a cadeia pré-compilada.
func (r *Router) executeRoute(route *Route, params map[string]string, w http.ResponseWriter, req *http.Request) {
	if params != nil {
		req = req.WithContext(context.WithValue(req.Context(), routeParamsContextKey{}, params))
	}
	if route.compiledHandler == nil {
		route.compiledHandler = compileRouteHTTPHandler(route)
	}
	route.compiledHandler.ServeHTTP(w, req)
}

// OpenAPIPaths retorna os caminhos OpenAPI do router.
func (r *Router) OpenAPIPaths() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	paths := make(map[string]any)
	for _, route := range r.allRoutes {
		if !route.IncludeInSchema {
			continue
		}
		pathItem, ok := paths[route.Path].(map[string]any)
		if !ok {
			pathItem = make(map[string]any)
		}
		responses := route.Responses
		if len(responses) == 0 {
			responses = map[string]any{strconv.Itoa(route.StatusCode): map[string]any{"description": "Successful response"}}
		}
		operation := map[string]any{
			"summary":     route.Summary,
			"description": route.Description,
			"responses":   responses,
		}
		if route.OperationID != "" {
			operation["operationId"] = route.OperationID
		}
		if route.Tags != nil {
			operation["tags"] = route.Tags
		}
		if route.Deprecated {
			operation["deprecated"] = true
		}
		if route.Security != nil {
			securityList := make([]any, len(route.Security))
			for i, s := range route.Security {
				m := make(map[string]any)
				for k, v := range s {
					m[k] = v
				}
				securityList[i] = m
			}
			operation["security"] = securityList
		}
		if route.Method != "WEBSOCKET" && route.Method != "MOUNT" {
			pathItem[strings.ToLower(route.Method)] = operation
			paths[route.Path] = pathItem
		}
	}
	return paths
}

// WebSocket registra uma rota de WebSocket no router.
func (r *Router) AddWebSocket(path string, handler any, opts ...any) {
	r.AddRoute("WEBSOCKET", path, handler, opts...)
}

// Include inclui as rotas de outro router. Sem opções, inclui o router sem prefixo.
func (r *Router) Include(router *Router, opts ...IncludeOption) {
	if router == nil || router == r {
		return
	}
	router.mu.RLock()
	source := append([]*Route(nil), router.allRoutes...)
	router.mu.RUnlock()
	if len(opts) == 0 {
		opts = []IncludeOption{{}}
	}
	for _, opt := range opts {
		for _, route := range source {
			newRoute := *route
			newRoute.Path = strings.TrimSuffix(opt.Prefix, "/") + route.Path
			if newRoute.Path == "" {
				newRoute.Path = "/"
			}
			newRoute.PathPattern = newRoute.Path
			newRoute.PathParams = extractParamNames(newRoute.Path)
			newRoute.PathRegex = compilePathRegex(newRoute.Path)
			newRoute.Tags = append(append([]string(nil), route.Tags...), opt.Tags...)
			newRoute.Dependencies = append(append([]Depends(nil), route.Dependencies...), opt.Dependencies...)
			newRoute.compiledHandler = compileRouteHTTPHandler(&newRoute)
			r.mu.Lock()
			r.routesByMethod[newRoute.Method] = append(r.routesByMethod[newRoute.Method], &newRoute)
			if len(newRoute.PathParams) == 0 {
				r.staticRoutes[routeKey(newRoute.Method, newRoute.Path)] = &newRoute
			} else {
				trie := r.routeTries[newRoute.Method]
				if trie == nil {
					trie = newRouteTrie()
					r.routeTries[newRoute.Method] = trie
				}
				trie.insert(newRoute.Path, newRoute.PathParams, &newRoute)
			}
			r.allRoutes = append(r.allRoutes, &newRoute)
			r.mu.Unlock()
		}
	}
}

// extractParamNames extrai os nomes dos parâmetros de caminho de um padrão {param}.
func extractParamNames(path string) []string {
	var paramNames []string
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			paramName := part[1 : len(part)-1]
			if idx := strings.Index(paramName, ":"); idx > 0 {
				paramName = paramName[:idx]
			}
			paramNames = append(paramNames, paramName)
		}
	}
	return paramNames
}

// compilePathRegex compila a expressão regular de um caminho de rota.
// Extraído para reutilização entre AddRoute e Include.
func compilePathRegex(path string) *regexp.Regexp {
	var paramNames []string
	regexStr := "^"
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if i > 0 {
			regexStr += "/"
		}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			paramName := part[1 : len(part)-1]
			if idx := strings.Index(paramName, ":"); idx > 0 {
				paramName = paramName[:idx]
				regexStr += "(?P<" + paramName + ">.+)"
			} else {
				regexStr += "(?P<" + paramName + ">[^/]+)"
			}
			paramNames = append(paramNames, paramName)
		} else {
			regexStr += regexp.QuoteMeta(part)
		}
	}
	regexStr += "$"
	return regexp.MustCompile(regexStr)
}

// Mount monta um http.Handler em um caminho.
func (r *Router) Mount(path string, h http.Handler) {
	if h == nil {
		return
	}
	path = "/" + strings.Trim(path, "/")
	if path == "/" {
		path = "/"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mounts = append(r.mounts, mountedHandler{prefix: path, handler: h})
	r.allRoutes = append(r.allRoutes, &Route{Method: "MOUNT", Path: path, mountHandler: h, IncludeInSchema: false})
}

// Mount representa um ponto de montagem (usado internamente).
type Mount struct {
	Path    string
	Handler http.Handler
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
