package gofast

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
)

var (
	errorDIProviderInvalid = errors.New("invalid dependency provider")
	errorDICycle           = errors.New("circular dependency detected")
)

type providerEntry struct {
	factory reflect.Value
	scope   Scope
}

// DIContainer é um container isolado por aplicação. Providers são indexados
// pelo tipo de retorno e inicializados de forma lazy.
type DIContainer struct {
	mu                    sync.RWMutex
	providers             map[reflect.Type][]providerEntry
	singletons            map[reflect.Type]any
	singletonErrors       map[reflect.Type]error
	singletonOnce         map[reflect.Type]*sync.Once
	singletonCleanups     map[reflect.Type]func() error
	singletonCleanupOrder []reflect.Type
	closed                bool
	logger                *slog.Logger
}

func NewDIContainer() *DIContainer {
	return &DIContainer{
		providers:         make(map[reflect.Type][]providerEntry),
		singletons:        make(map[reflect.Type]any),
		singletonErrors:   make(map[reflect.Type]error),
		singletonOnce:     make(map[reflect.Type]*sync.Once),
		singletonCleanups: make(map[reflect.Type]func() error),
		logger:            slog.Default(),
	}
}

// Provide registra um provider. O primeiro resultado define o tipo resolvido.
// São aceitas assinaturas que retornem T, (T, error), (T, func()) ou
// (T, func() error). O último resultado representa cleanup/lifespan.
func (d *DIContainer) Provide(provider any, opts ...ProvideOption) {
	if err := d.Register(provider, opts...); err != nil {
		d.logger.Error("DI provider registration failed", "error", err)
	}
}

// Register registra um provider e retorna erros de assinatura/configuração.
func (d *DIContainer) Register(provider any, opts ...ProvideOption) error {
	fv := reflect.ValueOf(provider)
	if !fv.IsValid() || fv.Kind() != reflect.Func {
		return fmt.Errorf("%w: provider must be a function", errorDIProviderInvalid)
	}
	ft := fv.Type()
	if ft.NumOut() < 1 || ft.NumOut() > 2 {
		return fmt.Errorf("%w: provider must return T or (T, error/cleanup)", errorDIProviderInvalid)
	}
	resultType := ft.Out(0)
	if resultType == reflect.TypeOf((*error)(nil)).Elem() {
		return fmt.Errorf("%w: first return value cannot be error", errorDIProviderInvalid)
	}
	if ft.NumOut() == 2 {
		second := ft.Out(1)
		errorType := reflect.TypeOf((*error)(nil)).Elem()
		cleanupValid := second.Kind() == reflect.Func && second.NumIn() == 0 && (second.NumOut() == 0 || (second.NumOut() == 1 && second.Out(0).Implements(errorType)))
		if !second.Implements(errorType) && !cleanupValid {
			return fmt.Errorf("%w: second return value must be error or func() / func() error", errorDIProviderInvalid)
		}
	}

	scope := ScopeRequest
	for _, opt := range opts {
		if opt.Scope != ScopeUnset {
			scope = opt.Scope
		}
	}
	if scope < ScopeSingleton || scope > ScopeTransient {
		return fmt.Errorf("invalid dependency scope %d", scope)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("dependency container is closed")
	}
	d.providers[resultType] = append(d.providers[resultType], providerEntry{factory: fv, scope: scope})
	if d.singletonOnce[resultType] == nil {
		d.singletonOnce[resultType] = &sync.Once{}
	}
	return nil
}

// Override substitui o provider do tipo original por um valor ou factory
// compatível. original pode ser um valor exemplar ou reflect.Type.
// Deve ser chamado durante a configuração da aplicação, antes de resolver
// dependências concorrentes. Se um Singleton já foi inicializado, seu cleanup
// é executado antes de o provider ser substituído.
func (d *DIContainer) Override(original, replacement any) {
	var originalType reflect.Type
	if t, ok := original.(reflect.Type); ok {
		originalType = t
	} else {
		originalType = reflect.TypeOf(original)
	}
	if originalType == nil {
		d.logger.Error("DI override skipped: original type is nil")
		return
	}

	var factory reflect.Value
	replacementValue := reflect.ValueOf(replacement)
	if replacementValue.IsValid() && replacementValue.Kind() == reflect.Func {
		if replacementValue.Type().NumOut() < 1 || !replacementValue.Type().Out(0).AssignableTo(originalType) {
			d.logger.Error("DI override skipped: replacement factory has incompatible return type", "original", originalType, "replacement", replacementValue.Type())
			return
		}
		factory = replacementValue
	} else {
		if !replacementValue.IsValid() || !replacementValue.Type().AssignableTo(originalType) {
			d.logger.Error("DI override skipped: replacement value is not assignable to original type", "original", originalType, "replacement", reflect.TypeOf(replacement))
			return
		}
		factoryType := reflect.FuncOf(nil, []reflect.Type{originalType}, false)
		factory = reflect.MakeFunc(factoryType, func([]reflect.Value) []reflect.Value {
			return []reflect.Value{replacementValue}
		})
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	oldCleanup := d.singletonCleanups[originalType]
	delete(d.singletonCleanups, originalType)
	if len(d.singletonCleanupOrder) > 0 {
		filtered := d.singletonCleanupOrder[:0]
		for _, typ := range d.singletonCleanupOrder {
			if typ != originalType {
				filtered = append(filtered, typ)
			}
		}
		d.singletonCleanupOrder = filtered
	}
	d.providers[originalType] = []providerEntry{{factory: factory, scope: ScopeTransient}}
	delete(d.singletons, originalType)
	delete(d.singletonErrors, originalType)
	d.singletonOnce[originalType] = &sync.Once{}
	d.mu.Unlock()
	if oldCleanup != nil {
		if err := oldCleanup(); err != nil {
			d.logger.Error("DI cleanup for overridden singleton failed", "type", originalType, "error", err)
		}
	}
	d.logger.Info("DI dependency overridden", "type", originalType)
}

// Resolve é a forma conveniente de resolução. Para tratamento explícito de
// falhas, use ResolveE.
func (d *DIContainer) Resolve(t reflect.Type, ctx *Context) any {
	value, err := d.ResolveE(t, ctx)
	if err != nil {
		d.logger.Error("DI resolution failed", "type", t, "error", err)
		return nil
	}
	return value
}

// ResolveE resolve uma dependência e propaga erros de factories/ciclos.
func (d *DIContainer) ResolveE(t reflect.Type, ctx *Context) (any, error) {
	return d.resolve(t, ctx, make(map[reflect.Type]bool))
}

func (d *DIContainer) resolve(t reflect.Type, ctx *Context, stack map[reflect.Type]bool) (any, error) {
	if t == nil {
		return nil, errors.New("cannot resolve a nil dependency type")
	}
	if stack[t] {
		return nil, fmt.Errorf("%w: %s", errorDICycle, t)
	}
	d.mu.RLock()
	entries := append([]providerEntry(nil), d.providers[t]...)
	closed := d.closed
	d.mu.RUnlock()
	if closed {
		return nil, errors.New("dependency container is closed")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no provider registered for %s", t)
	}
	entry := entries[0]

	nextStack := make(map[reflect.Type]bool, len(stack)+1)
	for typ, active := range stack {
		nextStack[typ] = active
	}
	nextStack[t] = true

	switch entry.scope {
	case ScopeSingleton:
		return d.resolveSingleton(t, entry, ctx, nextStack)
	case ScopeRequest:
		if ctx == nil {
			return nil, fmt.Errorf("request-scoped dependency %s requires a request Context", t)
		}
		if cached, ok := ctx.getDICache(t); ok {
			return cached, nil
		}
		value, cleanup, err := d.invokeFactory(entry, ctx, nextStack)
		if err != nil {
			return nil, err
		}
		ctx.setDICache(t, value)
		if cleanup != nil {
			ctx.addCleanup(cleanup)
		}
		return value, nil
	case ScopeTransient:
		value, cleanup, err := d.invokeFactory(entry, ctx, nextStack)
		if err != nil {
			return nil, err
		}
		if cleanup != nil && ctx != nil {
			ctx.addCleanup(cleanup)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported scope %d", entry.scope)
	}
}

func (d *DIContainer) resolveSingleton(t reflect.Type, entry providerEntry, ctx *Context, stack map[reflect.Type]bool) (any, error) {
	d.mu.Lock()
	once := d.singletonOnce[t]
	if once == nil {
		once = &sync.Once{}
		d.singletonOnce[t] = once
	}
	d.mu.Unlock()
	once.Do(func() {
		value, cleanup, err := d.invokeFactory(entry, ctx, stack)
		d.mu.Lock()
		if d.closed {
			d.singletonErrors[t] = errors.New("dependency container closed during singleton initialization")
			d.mu.Unlock()
			if cleanup != nil {
				if cleanupErr := cleanup(); cleanupErr != nil {
					d.logger.Error("DI singleton cleanup after concurrent close failed", "type", t, "error", cleanupErr)
				}
			}
			return
		}
		if err != nil {
			d.singletonErrors[t] = err
		} else {
			d.singletons[t] = value
			if cleanup != nil {
				d.singletonCleanups[t] = cleanup
				d.singletonCleanupOrder = append(d.singletonCleanupOrder, t)
			}
		}
		d.mu.Unlock()
	})
	d.mu.RLock()
	defer d.mu.RUnlock()
	if err := d.singletonErrors[t]; err != nil {
		return nil, err
	}
	value, ok := d.singletons[t]
	if !ok {
		return nil, fmt.Errorf("singleton provider for %s returned no value", t)
	}
	return value, nil
}

func (d *DIContainer) invokeFactory(entry providerEntry, ctx *Context, stack map[reflect.Type]bool) (value any, cleanup func() error, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			cleanup = nil
			err = fmt.Errorf("dependency provider panic: %v", recovered)
		}
	}()
	ft := entry.factory.Type()
	args := make([]reflect.Value, ft.NumIn())
	contextType := reflect.TypeOf((*Context)(nil))
	for i := 0; i < ft.NumIn(); i++ {
		paramType := ft.In(i)
		if paramType == contextType {
			if ctx == nil {
				args[i] = reflect.Zero(paramType)
			} else {
				args[i] = reflect.ValueOf(ctx)
			}
			continue
		}
		dep, resolveErr := d.resolve(paramType, ctx, stack)
		if resolveErr != nil {
			return nil, nil, fmt.Errorf("resolve provider argument %d (%s): %w", i, paramType, resolveErr)
		}
		depValue := reflect.ValueOf(dep)
		if !depValue.IsValid() || !depValue.Type().AssignableTo(paramType) {
			return nil, nil, fmt.Errorf("provider argument %d has incompatible dependency %s", i, paramType)
		}
		args[i] = depValue
	}
	results := entry.factory.Call(args)
	if len(results) == 0 {
		return nil, nil, errors.New("provider returned no values")
	}
	valueResult := results[0]
	if len(results) == 2 {
		second := results[1]
		if second.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			if err := reflectedError(second); err != nil {
				return nil, nil, err
			}
		} else if second.Kind() == reflect.Func && !second.IsNil() {
			cleanup = func() error {
				cleanupResults := second.Call(nil)
				if len(cleanupResults) == 1 {
					return reflectedError(cleanupResults[0])
				}
				return nil
			}
		}
	}
	if isNilable(valueResult.Kind()) && valueResult.IsNil() {
		return nil, nil, errors.New("provider returned a nil dependency")
	}
	value = valueResult.Interface()
	return value, cleanup, nil
}

// ClearRequestScope executa cleanups e limpa o estado DI da requisição quando
// recebe um Context. Sem argumento, é mantido como no-op por compatibilidade;
// o Context normalmente é descartado naturalmente ao fim da requisição.
func (d *DIContainer) ClearRequestScope(contexts ...*Context) {
	if len(contexts) == 0 || contexts[0] == nil {
		return
	}
	contexts[0].runCleanups()
	contexts[0].clearDICache()
}

// Close executa os cleanups singleton em ordem inversa e fecha o container.
func (d *DIContainer) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	cleanups := make([]func() error, 0, len(d.singletonCleanups))
	for i := len(d.singletonCleanupOrder) - 1; i >= 0; i-- {
		if cleanup := d.singletonCleanups[d.singletonCleanupOrder[i]]; cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	d.singletonCleanups = make(map[reflect.Type]func() error)
	d.singletonCleanupOrder = nil
	d.mu.Unlock()
	var firstErr error
	for _, cleanup := range cleanups {
		if err := cleanup(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// IsSingletonInitialized verifica se o singleton do tipo informado já existe.
func (d *DIContainer) IsSingletonInitialized(t reflect.Type) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, exists := d.singletons[t]
	return exists
}
