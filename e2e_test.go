package gofast

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// ---------- DI: Singleton scope ----------

func TestDI_Singleton(t *testing.T) {
	d := NewDIContainer()
	callCount := 0
	d.Provide(func() *counter { callCount++; return &counter{} }, Singleton())

	a := d.Resolve(reflect.TypeOf((*counter)(nil)), nil)
	b := d.Resolve(reflect.TypeOf((*counter)(nil)), nil)

	if a == nil || b == nil {
		t.Fatal("singleton deve retornar instância não-nula")
	}
	if a != b {
		t.Fatal("singleton deve retornar a mesma instância")
	}
	if callCount != 1 {
		t.Errorf("singleton factory deve ser chamado 1 vez, chamado %d", callCount)
	}
}

// ---------- DI: Request scope (leitura e gravação em ctx.state) ----------

func TestDI_RequestScope(t *testing.T) {
	d := NewDIContainer()
	callCount := 0
	d.Provide(func() *counter { callCount++; return &counter{} }, Request())

	ctx := NewContext(nil, &http.Request{}, nil)

	a := d.Resolve(reflect.TypeOf((*counter)(nil)), ctx)
	b := d.Resolve(reflect.TypeOf((*counter)(nil)), ctx)

	if a == nil || b == nil {
		t.Fatal("request scope deve retornar instância não-nula")
	}
	if a != b {
		t.Fatal("request scope deve retornar a mesma instância dentro da mesma requisição")
	}
	if callCount != 1 {
		t.Errorf("request factory deve ser chamado 1 vez, chamado %d", callCount)
	}
}

// ---------- DI: Transient scope ----------

func TestDI_Transient(t *testing.T) {
	d := NewDIContainer()
	d.Provide(func() *counterWithID { return &counterWithID{id: nextID()} }, Transient())

	aPtr := d.Resolve(reflect.TypeOf((*counterWithID)(nil)), nil)
	bPtr := d.Resolve(reflect.TypeOf((*counterWithID)(nil)), nil)

	a, ok := aPtr.(*counterWithID)
	if !ok {
		t.Fatalf("transient deve retornar *counterWithID, obteve %T", aPtr)
	}
	b, ok := bPtr.(*counterWithID)
	if !ok {
		t.Fatalf("transient deve retornar *counterWithID, obteve %T", bPtr)
	}

	if a == nil || b == nil {
		t.Fatal("transient deve retornar instância não-nula")
	}
	if a == b {
		t.Fatal("transient deve retornar instâncias diferentes")
	}
	if a.id == b.id {
		t.Fatal("transient deve retornar instâncias com IDs diferentes")
	}
}

var transientID int

func nextID() int {
	transientID++
	return transientID
}

type counterWithID struct {
	id int
}

// ---------- DI: ClearRequestScope não apaga singletons ----------

func TestDI_ClearRequestScope_KeepsSingletons(t *testing.T) {
	d := NewDIContainer()
	d.Provide(func() *counter { return &counter{} }, Singleton())

	inst1 := d.Resolve(reflect.TypeOf((*counter)(nil)), nil)
	d.ClearRequestScope()
	inst2 := d.Resolve(reflect.TypeOf((*counter)(nil)), nil)

	if inst1 != inst2 {
		t.Fatal("singleton deve persistir após ClearRequestScope")
	}
}

// ---------- Router: trie routing with path params ----------

func TestRouter_DynamicRouteParams(t *testing.T) {
	r := NewRouter()
	r.AddRoute("GET", "/usuarios/{id}", func(ctx *Context) (any, error) {
		return map[string]string{"id": ctx.Param("id")}, nil
	})

	handler := r.Handler(nil)
	req := httptest.NewRequest("GET", "/usuarios/123", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status esperado 200, obtido %d", w.Code)
	}
}

// ---------- Context: lazy initialization ----------

func TestContext_LazyInit(t *testing.T) {
	ctx := NewContext(nil, &http.Request{}, nil)

	if ctx.params != nil {
		t.Error("params deve ser nil inicialmente")
	}
	if ctx.state != nil {
		t.Error("state deve ser nil inicialmente")
	}
	if ctx.session != nil {
		t.Error("session deve ser nil inicialmente")
	}

	// SetParam deve inicializar params
	ctx.SetParam("id", "123")
	if ctx.params == nil {
		t.Error("params deve ser inicializado após SetParam")
	}
	if v := ctx.Param("id"); v != "123" {
		t.Errorf("param esperado '123', obtido '%s'", v)
	}

	// Session deve inicializar session
	s := ctx.Session()
	if s == nil {
		t.Error("session não deve ser nil após Session()")
	}
}

// ---------- Response: lazy headers ----------

func TestResponse_LazyHeaders(t *testing.T) {
	r := NewJSONResponse(map[string]string{"ok": "true"}, StatusOK)

	// Acessar headers diretamente (não deve panic com mapa nil)
	if r.headers != nil {
		t.Error("headers deve ser nil inicialmente")
	}

	// Render deve funcionar sem headers
	w := httptest.NewRecorder()
	if err := r.Render(w); err != nil {
		t.Errorf("Render não deve erro sem headers: %v", err)
	}

	// SetHeader deve inicializar headers
	r.SetHeader("X-Custom", "value")
	if r.headers == nil {
		t.Error("headers deve ser inicializado após SetHeader")
	}
	if r.headers["X-Custom"] != "value" {
		t.Errorf("header X-Custom esperado 'value', obtido '%s'", r.headers["X-Custom"])
	}
}

type counter struct{}
