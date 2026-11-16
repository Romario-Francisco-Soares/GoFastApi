package gofast

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenAPIGolden testa a geração do documento OpenAPI 3.1.
func TestOpenAPIGolden(t *testing.T) {
	app := New(
		Title("Test API"),
		Version("1.0.0"),
		Description("API de teste para golden test do OpenAPI"),
	)

	// Registrar rotas de teste
	app.GET("/usuarios", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"status": "ok"}, StatusOK), nil
	}, RouteOption{
		Tags:            []string{"usuarios"},
		Summary:         "Listar usuários",
		Description:     "Retorna a lista de todos os usuários cadastrados.",
		IncludeInSchema: true,
	})

	app.POST("/usuarios", func(ctx *Context) (any, error) {
		var body map[string]any
		if err := ctx.JSON(&body); err != nil {
			return NewJSONResponse(map[string]any{"error": "invalid body"}, StatusBadRequest), nil
		}
		return NewJSONResponse(map[string]any{"status": "created", "data": body}, StatusCreated), nil
	}, RouteOption{
		Tags:            []string{"usuarios"},
		Summary:         "Criar usuário",
		Description:     "Cria um novo usuário com os dados fornecidos.",
		IncludeInSchema: true,
		Security: []SecurityRequirement{
			{"BearerAuth": []string{}},
		},
	})

	app.GET("/usuarios/{id}", func(ctx *Context) (any, error) {
		id := ctx.Param("id")
		return NewJSONResponse(map[string]any{"id": id, "nome": "Usuario " + id}, StatusOK), nil
	}, RouteOption{
		Tags:            []string{"usuarios"},
		Summary:         "Obter usuário por ID",
		Description:     "Retorna os dados de um usuário específico pelo seu ID.",
		IncludeInSchema: true,
	})

	// Adicionar security scheme Bearer
	app.AddSecurityScheme("BearerAuth", SecurityScheme{
		Type:         "http",
		Scheme:       "bearer",
		BearerFormat: "JWT",
		Description:  "Autenticação via Bearer token JWT",
	})

	// Debug: verificar rotas registradas
	t.Logf("Número de rotas registradas: %d", app.RoutesCount())
	for _, p := range app.RoutePaths() {
		t.Logf("  Rota: %s", p)
	}

	// Gerar documento OpenAPI
	doc := app.OpenAPI()

	// Debug: imprimir número de paths
	t.Logf("Número de paths no OpenAPI: %d", len(doc.Paths))
	for p := range doc.Paths {
		t.Logf("  Path OpenAPI: %s", p)
	}

	// Validar estrutura básica
	if doc.OpenAPI != "3.1.0" {
		t.Errorf("OpenAPI version deve ser 3.1.0, obtido: %s", doc.OpenAPI)
	}
	if doc.Info.Title != "Test API" {
		t.Errorf("Info.Title deve ser 'Test API', obtido: %s", doc.Info.Title)
	}
	if doc.Info.Version != "1.0.0" {
		t.Errorf("Info.Version deve ser '1.0.0', obtido: %s", doc.Info.Version)
	}

	// Validar paths
	if _, ok := doc.Paths["/usuarios"]; !ok {
		t.Error("Path /usuarios não encontrado no OpenAPI")
	}
	if _, ok := doc.Paths["/usuarios/{id}"]; !ok {
		t.Error("Path /usuarios/{id} não encontrado no OpenAPI")
	}

	// Validar security schemes
	if doc.Components == nil {
		t.Error("Components não deve ser nil")
	} else if len(doc.Components.SecuritySchemes) == 0 {
		t.Error("SecuritySchemes não deve estar vazio")
	} else {
		scheme, ok := doc.Components.SecuritySchemes["BearerAuth"]
		if !ok {
			t.Error("SecurityScheme 'BearerAuth' não encontrado")
		} else {
			if scheme.Type != "http" {
				t.Errorf("SecurityScheme Type deve ser 'http', obtido: %s", scheme.Type)
			}
			if scheme.Scheme != "bearer" {
				t.Errorf("SecurityScheme Scheme deve ser 'bearer', obtido: %s", scheme.Scheme)
			}
		}
	}

	// Serializar para JSON para verificar formato
	docJSON, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Erro ao serializar OpenAPI para JSON: %v", err)
	}

	// Verificar se contém campos essenciais
	jsonStr := string(docJSON)
	if !strings.Contains(jsonStr, `"openapi":"3.1.0"`) {
		t.Error("JSON OpenAPI deve conter openapi 3.1.0")
	}
	if !strings.Contains(jsonStr, `"title":"Test API"`) {
		t.Error("JSON OpenAPI deve conter title 'Test API'")
	}
	if !strings.Contains(jsonStr, `"BearerAuth"`) {
		t.Error("JSON OpenAPI deve conter security scheme 'BearerAuth'")
	}
	if !strings.Contains(jsonStr, `"security"`) {
		t.Error("JSON OpenAPI deve conter security requirements")
	}

	// Salvar arquivo dourado para comparação futura
	goldenDir := filepath.Join("testdata", "openapi")
	if err := os.MkdirAll(goldenDir, 0755); err != nil {
		t.Fatalf("Erro ao criar diretório testdata: %v", err)
	}
	goldenPath := filepath.Join(goldenDir, "openapi_golden.json")
	if err := os.WriteFile(goldenPath, docJSON, 0644); err != nil {
		t.Fatalf("Erro ao salvar arquivo dourado: %v", err)
	}
	t.Logf("Arquivo dourado salvo em: %s", goldenPath)
}

// TestOpenAPIEndpoint testa o endpoint /openapi.json via HTTP.
func TestOpenAPIEndpoint(t *testing.T) {
	app := New(
		Title("Endpoint Test API"),
		Version("2.0.0"),
	)

	app.GET("/test", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"ok": true}, StatusOK), nil
	})

	// Criar servidor de teste
	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	// Fazer requisição para /openapi.json
	resp, err := http.Get(server.URL + "/openapi.json")
	if err != nil {
		t.Fatalf("Erro ao acessar /openapi.json: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.json deve retornar 200, obtido: %d", resp.StatusCode)
	}

	// Decodificar resposta
	var doc OpenAPIDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("Erro ao decodificar resposta OpenAPI: %v", err)
	}

	if doc.Info.Title != "Endpoint Test API" {
		t.Errorf("Title deve ser 'Endpoint Test API', obtido: %s", doc.Info.Title)
	}
	if doc.Info.Version != "2.0.0" {
		t.Errorf("Version deve ser '2.0.0', obtido: %s", doc.Info.Version)
	}
}

// TestOpenAPISwaggerUI testa o endpoint /docs (Swagger UI).
func TestOpenAPISwaggerUI(t *testing.T) {
	app := New(
		Title("Swagger Test API"),
		Version("1.0.0"),
	)

	app.GET("/test", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"ok": true}, StatusOK), nil
	})

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/docs")
	if err != nil {
		t.Fatalf("Erro ao acessar /docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("/docs deve retornar 200, obtido: %d", resp.StatusCode)
	}

	body := make([]byte, resp.ContentLength)
	resp.Body.Read(body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "swagger-ui") {
		t.Error("Resposta /docs deve conter Swagger UI")
	}
}

// TestOpenAPIReDoc testa o endpoint /redoc (ReDoc).
func TestOpenAPIReDoc(t *testing.T) {
	app := New(
		Title("ReDoc Test API"),
		Version("1.0.0"),
	)

	app.GET("/test", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"ok": true}, StatusOK), nil
	})

	handler := app.Handler()
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/redoc")
	if err != nil {
		t.Fatalf("Erro ao acessar /redoc: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("/redoc deve retornar 200, obtido: %d", resp.StatusCode)
	}

	body := make([]byte, resp.ContentLength)
	resp.Body.Read(body)
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "redoc") {
		t.Error("Resposta /redoc deve conter ReDoc")
	}
}

// TestOpenAPISecuritySchemes testa diferentes tipos de security schemes.
func TestOpenAPISecuritySchemes(t *testing.T) {
	app := New(
		Title("Security Schemes Test"),
		Version("1.0.0"),
	)

	// API Key no header
	app.AddSecurityScheme("ApiKeyHeader", SecurityScheme{
		Type:        "apiKey",
		Name:        "X-API-Key",
		In:          "header",
		Description: "API Key no header",
	})

	// API Key no query
	app.AddSecurityScheme("ApiKeyQuery", SecurityScheme{
		Type:        "apiKey",
		Name:        "api_key",
		In:          "query",
		Description: "API Key no query string",
	})

	// HTTP Basic
	app.AddSecurityScheme("BasicAuth", SecurityScheme{
		Type:        "http",
		Scheme:      "basic",
		Description: "Autenticação HTTP Basic",
	})

	// OAuth2 Password
	app.AddSecurityScheme("OAuth2Password", SecurityScheme{
		Type: "oauth2",
		Flows: &OAuthFlows{
			Password: &OAuthFlow{
				TokenUrl: "https://example.com/token",
				Scopes: map[string]string{
					"read":  "Acesso de leitura",
					"write": "Acesso de escrita",
				},
			},
		},
		Description: "OAuth2 com fluxo password",
	})

	// OpenID Connect
	app.AddSecurityScheme("OpenIdConnect", SecurityScheme{
		Type:             "openIdConnect",
		OpenIdConnectUrl: "https://example.com/.well-known/openid-configuration",
		Description:      "OpenID Connect discovery",
	})

	doc := app.OpenAPI()

	if doc.Components == nil {
		t.Fatal("Components não deve ser nil")
	}

	schemes := doc.Components.SecuritySchemes
	expectedSchemes := []string{"ApiKeyHeader", "ApiKeyQuery", "BasicAuth", "OAuth2Password", "OpenIdConnect"}
	for _, name := range expectedSchemes {
		if _, ok := schemes[name]; !ok {
			t.Errorf("SecurityScheme '%s' não encontrado", name)
		}
	}

	// Validar ApiKeyHeader
	scheme := schemes["ApiKeyHeader"]
	if scheme.Type != "apiKey" {
		t.Errorf("ApiKeyHeader Type deve ser 'apiKey', obtido: %s", scheme.Type)
	}
	if scheme.Name != "X-API-Key" {
		t.Errorf("ApiKeyHeader Name deve ser 'X-API-Key', obtido: %s", scheme.Name)
	}
	if scheme.In != "header" {
		t.Errorf("ApiKeyHeader In deve ser 'header', obtido: %s", scheme.In)
	}

	// Validar OAuth2Password
	scheme = schemes["OAuth2Password"]
	if scheme.Type != "oauth2" {
		t.Errorf("OAuth2Password Type deve ser 'oauth2', obtido: %s", scheme.Type)
	}
	if scheme.Flows == nil {
		t.Error("OAuth2Password Flows não deve ser nil")
	} else if scheme.Flows.Password == nil {
		t.Error("OAuth2Password Flows.Password não deve ser nil")
	} else {
		if scheme.Flows.Password.TokenUrl != "https://example.com/token" {
			t.Errorf("OAuth2Password TokenUrl incorreto: %s", scheme.Flows.Password.TokenUrl)
		}
		if len(scheme.Flows.Password.Scopes) != 2 {
			t.Errorf("OAuth2Password deve ter 2 scopes, obtido: %d", len(scheme.Flows.Password.Scopes))
		}
	}

	// Validar OpenID Connect
	scheme = schemes["OpenIdConnect"]
	if scheme.Type != "openIdConnect" {
		t.Errorf("OpenIdConnect Type deve ser 'openIdConnect', obtido: %s", scheme.Type)
	}
	if scheme.OpenIdConnectUrl != "https://example.com/.well-known/openid-configuration" {
		t.Errorf("OpenIdConnectUrl incorreto: %s", scheme.OpenIdConnectUrl)
	}
}

// TestOpenAPIRouteSecurity testa security requirements por rota.
func TestOpenAPIRouteSecurity(t *testing.T) {
	app := New(
		Title("Route Security Test"),
		Version("1.0.0"),
	)

	app.AddSecurityScheme("BearerAuth", SecurityScheme{
		Type:   "http",
		Scheme: "bearer",
	})

	app.GET("/public", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"public": true}, StatusOK), nil
	}, RouteOption{
		IncludeInSchema: true,
	})

	app.GET("/private", func(ctx *Context) (any, error) {
		return NewJSONResponse(map[string]any{"private": true}, StatusOK), nil
	}, RouteOption{
		IncludeInSchema: true,
		Security: []SecurityRequirement{
			{"BearerAuth": []string{}},
		},
	})

	doc := app.OpenAPI()

	t.Logf("Número de rotas registradas: %d", app.RoutesCount())
	for _, p := range app.RoutePaths() {
		t.Logf("  Rota: %s", p)
	}

	t.Logf("Número de paths no OpenAPI: %d", len(doc.Paths))
	for p := range doc.Paths {
		t.Logf("  Path OpenAPI: %s", p)
	}

	// Verificar rota pública (sem security)
	publicPathVal := doc.Paths["/public"]
	if publicPathVal == nil {
		t.Error("Path /public não encontrado")
	} else {
		publicPath, ok := publicPathVal.(map[string]any)
		if !ok {
			t.Error("Path /public não é um mapa")
		} else {
			getOpVal, ok := publicPath["get"]
			if !ok {
				t.Error("Operação GET /public não encontrada")
			} else {
				getOp, ok := getOpVal.(map[string]any)
				if !ok {
					t.Error("Operação GET /public não é um mapa")
				} else {
					if _, hasSecurity := getOp["security"]; hasSecurity {
						t.Error("Rota pública não deve ter security requirement")
					}
				}
			}
		}
	}

	// Verificar rota privada (com security)
	privatePathVal := doc.Paths["/private"]
	if privatePathVal == nil {
		t.Error("Path /private não encontrado")
	} else {
		privatePath, ok := privatePathVal.(map[string]any)
		if !ok {
			t.Error("Path /private não é um mapa")
		} else {
			getOpVal, ok := privatePath["get"]
			if !ok {
				t.Error("Operação GET /private não encontrada")
			} else {
				getOp, ok := getOpVal.(map[string]any)
				if !ok {
					t.Error("Operação GET /private não é um mapa")
				} else {
					security, hasSecurity := getOp["security"]
					if !hasSecurity {
						t.Error("Rota privada deve ter security requirement")
					} else {
						t.Logf("DEBUG security value: %#v (type: %T)", security, security)
						securityList, ok := security.([]any)
						if !ok {
							t.Errorf("Security requirement deve ser []any, obtido: %T", security)
						} else if len(securityList) != 1 {
							t.Errorf("Security requirement deve ser uma lista com 1 item, obtido: %d itens: %#v", len(securityList), securityList)
						} else {
							req, ok := securityList[0].(map[string]any)
							if !ok {
								t.Error("Security requirement item deve ser um mapa")
							} else if _, hasBearer := req["BearerAuth"]; !hasBearer {
								t.Error("Security requirement deve conter BearerAuth")
							}
						}
					}
				}
			}
		}
	}
}
