package gofast

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

type principalContextKey struct{}

// Principal representa a identidade autenticada disponibilizada ao handler.
type Principal struct {
	Subject    string         `json:"sub,omitempty"`
	Name       string         `json:"name,omitempty"`
	Scopes     []string       `json:"scopes,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// PrincipalFromRequest recupera a identidade autenticada do request.
func PrincipalFromRequest(r *http.Request) (*Principal, bool) {
	p, ok := r.Context().Value(principalContextKey{}).(*Principal)
	return p, ok && p != nil
}

// Context.Principal permite acessar a identidade autenticada no handler.
func (c *Context) Principal() (*Principal, bool) { return PrincipalFromRequest(c.req) }

type APIKeyLocation string

const (
	APIKeyHeader APIKeyLocation = "header"
	APIKeyQuery  APIKeyLocation = "query"
	APIKeyCookie APIKeyLocation = "cookie"
)

type CredentialValidator func(*http.Request, string) (*Principal, error)
type BasicCredentialValidator func(*http.Request, string, string) (*Principal, error)

// APIKeyConfig configura autenticação por chave em header, query ou cookie.
type APIKeyConfig struct {
	Name      string
	In        APIKeyLocation
	Secret    string
	Validator CredentialValidator
}

// APIKey cria middleware de autenticação por API Key. Prefira header em APIs
// públicas; query strings podem aparecer em logs e históricos de navegação.
func APIKey(cfg APIKeyConfig) Middleware {
	if cfg.Name == "" {
		cfg.Name = "X-API-Key"
	}
	if cfg.In == "" {
		cfg.In = APIKeyHeader
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var credential string
			switch cfg.In {
			case APIKeyHeader:
				credential = r.Header.Get(cfg.Name)
			case APIKeyQuery:
				credential = r.URL.Query().Get(cfg.Name)
			case APIKeyCookie:
				if c, err := r.Cookie(cfg.Name); err == nil {
					credential = c.Value
				}
			default:
				writeAuthError(w, http.StatusInternalServerError, "invalid API key configuration", "")
				return
			}
			principal, ok := validateCredential(r, credential, cfg.Secret, cfg.Validator)
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "invalid or missing API key", "")
				return
			}
			if principal == nil {
				principal = &Principal{Subject: "api-key"}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
		})
	}
}

// BearerConfig configura autenticação HTTP Bearer.
type BearerConfig struct {
	Secret    string
	Validator CredentialValidator
	Realm     string
}

// HTTPBearer valida credenciais Authorization: Bearer.
func HTTPBearer(cfg BearerConfig) Middleware {
	if cfg.Realm == "" {
		cfg.Realm = "Bearer"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kind, token, ok := parseAuthorization(r.Header.Get("Authorization"))
			if !ok || !strings.EqualFold(kind, "Bearer") {
				writeAuthError(w, http.StatusUnauthorized, "bearer token required", `Bearer realm="`+cfg.Realm+`"`)
				return
			}
			principal, valid := validateCredential(r, token, cfg.Secret, cfg.Validator)
			if !valid {
				writeAuthError(w, http.StatusUnauthorized, "invalid bearer token", `Bearer realm="`+cfg.Realm+`", error="invalid_token"`)
				return
			}
			if principal == nil {
				principal = &Principal{Subject: "bearer"}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
		})
	}
}

// BasicConfig configura autenticação HTTP Basic.
type BasicConfig struct {
	Username  string
	Password  string
	Validator BasicCredentialValidator
	Realm     string
}

// HTTPBasic valida credenciais Authorization: Basic usando comparação em
// tempo constante quando Username/Password estático é configurado.
func HTTPBasic(cfg BasicConfig) Middleware {
	if cfg.Realm == "" {
		cfg.Realm = "GoFastAPI"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			kind, encoded, ok := parseAuthorization(r.Header.Get("Authorization"))
			if !ok || !strings.EqualFold(kind, "Basic") {
				writeAuthError(w, http.StatusUnauthorized, "basic credentials required", `Basic realm="`+cfg.Realm+`"`)
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid credentials", `Basic realm="`+cfg.Realm+`"`)
				return
			}
			username, password, found := strings.Cut(string(decoded), ":")
			if !found {
				writeAuthError(w, http.StatusUnauthorized, "invalid credentials", `Basic realm="`+cfg.Realm+`"`)
				return
			}
			var principal *Principal
			valid := false
			if cfg.Validator != nil {
				principal, err = cfg.Validator(r, username, password)
				valid = err == nil && principal != nil
			} else {
				uOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Username)) == 1
				pOK := subtle.ConstantTimeCompare([]byte(password), []byte(cfg.Password)) == 1
				valid = cfg.Username != "" && cfg.Password != "" && uOK && pOK
			}
			if !valid {
				writeAuthError(w, http.StatusUnauthorized, "invalid credentials", `Basic realm="`+cfg.Realm+`"`)
				return
			}
			if principal == nil {
				principal = &Principal{Subject: username, Name: username}
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
		})
	}
}

// OAuth2Bearer reutiliza o fluxo Bearer para validadores de tokens OAuth2.
// A emissão, assinatura e rotação de tokens permanecem a cargo de um provedor.
func OAuth2Bearer(validate CredentialValidator, realm string) Middleware {
	return HTTPBearer(BearerConfig{Validator: validate, Realm: realm})
}

// APIKeyScheme cria metadados OpenAPI para API Key.
func APIKeyScheme(name string, in APIKeyLocation, description string) SecurityScheme {
	return SecurityScheme{Type: "apiKey", Name: name, In: string(in), Description: description}
}

// HTTPBearerScheme cria metadados OpenAPI para HTTP Bearer.
func HTTPBearerScheme(format string) SecurityScheme {
	return SecurityScheme{Type: "http", Scheme: "bearer", BearerFormat: format}
}

// OpenIDConnectScheme cria metadados OpenAPI para OpenID Connect.
func OpenIDConnectScheme(discoveryURL string) SecurityScheme {
	return SecurityScheme{Type: "openIdConnect", OpenIdConnectUrl: discoveryURL}
}

func validateCredential(r *http.Request, credential, secret string, validator CredentialValidator) (*Principal, bool) {
	if credential == "" {
		return nil, false
	}
	if validator != nil {
		p, err := validator(r, credential)
		return p, err == nil && p != nil
	}
	return nil, secret != "" && subtle.ConstantTimeCompare([]byte(credential), []byte(secret)) == 1
}

func parseAuthorization(value string) (string, string, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeAuthError(w http.ResponseWriter, status int, message, challenge string) {
	if challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
