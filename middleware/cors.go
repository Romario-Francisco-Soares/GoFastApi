package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORSConfig configura o middleware de CORS (Cross-Origin Resource Sharing).
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	ExposeHeaders    []string
	MaxAge           int
}

// CORS aplica CORS com validação de origem e preflight. Quando credentials é
// habilitado, nunca emite Access-Control-Allow-Origin: *; a origem permitida é
// refletida e Vary: Origin é adicionado.
func CORS(config CORSConfig) func(http.Handler) http.Handler {
	origins := make(map[string]struct{}, len(config.AllowOrigins))
	allowAll := len(config.AllowOrigins) == 0
	for _, origin := range config.AllowOrigins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			allowAll = true
		} else if origin != "" {
			origins[origin] = struct{}{}
		}
	}

	methods := config.AllowMethods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}
	}
	allowedMethods := make(map[string]struct{}, len(methods))
	allowAnyMethod := false
	for _, method := range methods {
		method = strings.ToUpper(strings.TrimSpace(method))
		if method == "*" {
			allowAnyMethod = true
		} else if method != "" {
			allowedMethods[method] = struct{}{}
		}
	}

	headers := config.AllowHeaders
	if len(headers) == 0 {
		headers = []string{"Accept", "Accept-Encoding", "Authorization", "Content-Type", "Origin", "User-Agent", "X-Requested-With"}
	}
	allowedHeaders := make(map[string]struct{}, len(headers))
	allowAnyHeader := false
	for _, header := range headers {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "*" {
			allowAnyHeader = true
		} else if header != "" {
			allowedHeaders[header] = struct{}{}
		}
	}
	allowHeaderValue := strings.Join(headers, ", ")
	allowMethodValue := strings.Join(methods, ", ")
	exposeHeaderValue := strings.Join(config.ExposeHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := strings.TrimSpace(r.Header.Get("Origin"))
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			_, exact := origins[origin]
			allowedOrigin := allowAll || exact
			if !allowedOrigin {
				if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			if allowAll && !config.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				addVary(w.Header(), "Origin")
			}
			if config.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if exposeHeaderValue != "" {
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaderValue)
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
				if _, ok := allowedMethods[requestedMethod]; !ok && !allowAnyMethod {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				requestedHeaders := make([]string, 0)
				if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
					for _, header := range strings.Split(reqHeaders, ",") {
						header = strings.ToLower(strings.TrimSpace(header))
						if _, ok := allowedHeaders[header]; !ok && !allowAnyHeader {
							w.WriteHeader(http.StatusForbidden)
							return
						}
						if header != "" {
							requestedHeaders = append(requestedHeaders, header)
						}
					}
				}
				methodsValue := allowMethodValue
				if allowAnyMethod {
					methodsValue = requestedMethod
				}
				headersValue := allowHeaderValue
				if allowAnyHeader {
					// With credentials, wildcard is not a wildcard in the Fetch CORS
					// algorithm. Echo the requested header names explicitly.
					headersValue = strings.Join(requestedHeaders, ", ")
				}
				w.Header().Set("Access-Control-Allow-Methods", methodsValue)
				if headersValue != "" {
					w.Header().Set("Access-Control-Allow-Headers", headersValue)
				}
				if config.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(config.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
