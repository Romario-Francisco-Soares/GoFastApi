package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ProxyConfig configura o middleware de proxy reverso.
type ProxyConfig struct {
	ProxyHeaders      bool
	TrustedProxies    []string // CIDRs ou IPs confiáveis
	ForwardedHeaders  []string // Headers a confiar (Forwarded, X-Forwarded-*)
	ForwardedAllowIPs []string // IPs permitidos para Forwarded header
	RootPath          string   // Caminho base para publicação sob prefixo
	BasePath          string   // Alias para RootPath
}

// trustedNetworks armazena as redes confiáveis parseadas.
type trustedNetworks struct {
	nets []*net.IPNet
	ips  []net.IP
}

// parseTrustedProxies parseia a lista de proxies confiáveis em redes e IPs.
func parseTrustedProxies(proxies []string) *trustedNetworks {
	tn := &trustedNetworks{}
	for _, p := range proxies {
		if strings.Contains(p, "/") {
			_, net, err := net.ParseCIDR(p)
			if err == nil {
				tn.nets = append(tn.nets, net)
			}
		} else {
			ip := net.ParseIP(p)
			if ip != nil {
				tn.ips = append(tn.ips, ip)
			}
		}
	}
	return tn
}

// isTrusted verifica se um IP é confiável.
func (tn *trustedNetworks) isTrusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, ip2 := range tn.ips {
		if ip2.Equal(ip) {
			return true
		}
	}
	for _, net := range tn.nets {
		if net.Contains(ip) {
			return true
		}
	}
	return false
}

// Proxy middleware processa headers de proxy reverso (equivalente a TrustedProxies + ForwardedHeaders do Uvicorn).
func Proxy(config ProxyConfig) func(http.Handler) http.Handler {
	tn := parseTrustedProxies(config.TrustedProxies)

	// Determinar quais headers de forwarded confiar
	trustForwarded := false
	trustXForwarded := false
	for _, h := range config.ForwardedHeaders {
		switch strings.ToLower(h) {
		case "forwarded":
			trustForwarded = true
		case "x-forwarded-for", "x-forwarded-proto", "x-forwarded-host", "x-forwarded-port":
			trustXForwarded = true
		}
	}

	rootPath := config.RootPath
	if rootPath == "" {
		rootPath = config.BasePath
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Obter IP remoto do cliente
			clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				clientIP = r.RemoteAddr
			}
			ip := net.ParseIP(clientIP)

			// Se proxy headers estão habilitados e o IP é confiável, processar headers
			if config.ProxyHeaders && ip != nil && tn.isTrusted(ip) {
				// Processar Forwarded header (RFC 7239)
				if trustForwarded {
					forwarded := r.Header.Get("Forwarded")
					if forwarded != "" {
						// Parse simplificado do Forwarded header
						if proto := extractForwardedParam(forwarded, "proto"); proto != "" {
							r.URL.Scheme = proto
						}
						if host := extractForwardedParam(forwarded, "host"); host != "" {
							r.Host = host
						}
						if forParam := extractForwardedParam(forwarded, "for"); forParam != "" {
							// Remover porta se presente
							if idx := strings.LastIndex(forParam, ":"); idx > 0 {
								forParam = forParam[:idx]
							}
							r.RemoteAddr = forParam + ":0"
						}
					}
				}

				// Processar X-Forwarded-*
				if trustXForwarded {
					if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
						r.URL.Scheme = proto
					}
					if host := r.Header.Get("X-Forwarded-Host"); host != "" {
						r.Host = host
					}
					if forHeader := r.Header.Get("X-Forwarded-For"); forHeader != "" {
						// X-Forwarded-For pode conter múltiplos IPs separados por vírgula
						ips := strings.Split(forHeader, ",")
						if len(ips) > 0 {
							clientIP := strings.TrimSpace(ips[0])
							if idx := strings.LastIndex(clientIP, ":"); idx > 0 {
								clientIP = clientIP[:idx]
							}
							r.RemoteAddr = clientIP + ":0"
						}
					}
				}
			}

			// Aplicar root path se configurado
			if rootPath != "" && strings.HasPrefix(r.URL.Path, rootPath) {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, rootPath)
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractForwardedParam extrai um parâmetro específico do header Forwarded.
func extractForwardedParam(forwarded, param string) string {
	// Formato: proto=https;host=example.com;for=192.168.1.1
	pairs := strings.Split(forwarded, ";")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], param) {
			value := strings.Trim(kv[1], `"`)
			return value
		}
	}
	return ""
}

// TrustedHost middleware valida o Host header contra uma lista de hosts permitidos.
func TrustedHost(allowedHosts []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			// Remover porta se presente
			if idx := strings.LastIndex(host, ":"); idx > 0 {
				host = host[:idx]
			}

			allowed := false
			for _, h := range allowedHosts {
				if h == "*" || h == host {
					allowed = true
					break
				}
			}

			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"Forbidden: invalid host"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// HTTPSRedirect redireciona requisições HTTP para HTTPS.
func HTTPSRedirect() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Scheme != "https" && r.TLS == nil {
				// Verificar X-Forwarded-Proto
				if r.Header.Get("X-Forwarded-Proto") == "https" {
					next.ServeHTTP(w, r)
					return
				}
				// Redirecionar para HTTPS
				target := "https://" + r.Host + r.URL.Path
				if r.URL.RawQuery != "" {
					target += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, target, http.StatusPermanentRedirect)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
