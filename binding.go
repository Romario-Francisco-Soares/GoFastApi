package gofast

import (
	"fmt"
	"reflect"
	"strconv"
)

// Binding representa um binding de parâmetro, resolvido no startup.
type Binding struct {
	Source   string // path, query, header, cookie, json, form, file
	Name     string
	Kind     reflect.Type
	Required bool
	Default  any
}

// CompileBinding compila a assinatura de um handler em bindings.
// Isso é chamado uma vez no registro da rota, não por requisição.
func CompileBinding(handler any) ([]Binding, error) {
	t := reflect.TypeOf(handler)
	if t.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function")
	}

	var bindings []Binding
	for i := 0; i < t.NumIn(); i++ {
		paramType := t.In(i)
		paramName := fmt.Sprintf("arg%d", i)

		// Pular *Context (é injetado automaticamente)
		if paramType == reflect.TypeOf((*Context)(nil)) {
			continue
		}

		// Pular Depends (é resolvido via DI)
		if paramType == reflect.TypeOf(Depends{}) {
			continue
		}

		binding := Binding{
			Source:   "auto",
			Name:     paramName,
			Kind:     paramType,
			Required: true,
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

// BindRequest vincula parâmetros de uma requisição aos parâmetros do handler.
// Esta função é chamada no hot path, mas recebe os bindings pré-compilados.
func BindRequest(ctx *Context, bindings []Binding, handler any) ([]reflect.Value, error) {
	t := reflect.TypeOf(handler)

	argIndex := 0
	args := make([]reflect.Value, t.NumIn())

	for i := 0; i < t.NumIn(); i++ {
		paramType := t.In(i)

		// Pular *Context
		if paramType == reflect.TypeOf((*Context)(nil)) {
			args[i] = reflect.ValueOf(ctx)
			argIndex++
			continue
		}

		// Pular Depends - resolvido via DI container
		if paramType == reflect.TypeOf(Depends{}) {
			dep := diContainerGlobal.Resolve(paramType, ctx)
			args[i] = reflect.ValueOf(dep)
			argIndex++
			continue
		}

		// Para map[string]any (body genérico)
		if paramType == reflect.TypeOf(map[string]any{}) {
			var body map[string]any
			if err := ctx.JSON(&body); err != nil {
				body = make(map[string]any)
			}
			args[i] = reflect.ValueOf(body)
			argIndex++
			continue
		}

		// Para []any (lista genérica)
		if paramType == reflect.TypeOf([]any{}) {
			var body []any
			if err := ctx.JSON(&body); err != nil {
				body = []any{}
			}
			args[i] = reflect.ValueOf(body)
			argIndex++
			continue
		}

		// Para string (path param ou query)
		if paramType == reflect.TypeOf("") {
			val := ctx.Param(bindings[argIndex].Name)
			if val == "" {
				val = ctx.Query(bindings[argIndex].Name)
			}
			args[i] = reflect.ValueOf(val)
			argIndex++
			continue
		}

		// Para int
		if paramType == reflect.TypeOf(0) {
			valStr := ctx.Param(bindings[argIndex].Name)
			if valStr == "" {
				valStr = ctx.Query(bindings[argIndex].Name)
			}
			val, err := strconv.Atoi(valStr)
			if err != nil {
				val = 0
			}
			args[i] = reflect.ValueOf(val)
			argIndex++
			continue
		}

		// Para *string (opcional)
		if paramType == reflect.TypeOf((*string)(nil)) {
			val := ctx.Param(bindings[argIndex].Name)
			if val == "" {
				val = ctx.Query(bindings[argIndex].Name)
			}
			if val == "" {
				args[i] = reflect.Zero(paramType)
			} else {
				args[i] = reflect.ValueOf(&val)
			}
			argIndex++
			continue
		}

		// Para struct Pydantic-like
		if paramType.Kind() == reflect.Struct || paramType.Kind() == reflect.Ptr {
			var instance any
			if paramType.Kind() == reflect.Ptr {
				instance = reflect.New(paramType.Elem()).Interface()
			} else {
				instance = reflect.New(paramType).Interface()
			}
			if err := ctx.JSON(instance); err != nil {
				return nil, fmt.Errorf("failed to bind body: %w", err)
			}
			args[i] = reflect.ValueOf(instance).Elem()
			argIndex++
			continue
		}

		// Para UploadFile
		if paramType == reflect.TypeOf(UploadFile{}) {
			uf, err := ctx.FormFile("file")
			if err != nil {
				return nil, fmt.Errorf("failed to get uploaded file: %w", err)
			}
			args[i] = reflect.ValueOf(*uf)
			argIndex++
			continue
		}

		// Default: tentar JSON
		var instance = reflect.New(paramType).Interface()
		if err := ctx.JSON(instance); err != nil {
			return nil, fmt.Errorf("failed to bind parameter %d: %w", i, err)
		}
		args[i] = reflect.ValueOf(instance).Elem()
		argIndex++
	}

	return args, nil
}

// diContainerGlobal é mantido para compatibilidade com o BindRequest.
// Em produção, cada App tem seu próprio DI container acessível via app.DI().
var diContainerGlobal = NewDIContainer()
