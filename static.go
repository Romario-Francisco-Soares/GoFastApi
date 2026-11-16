package gofast

import (
	"net/http"
	"strings"
)

// StaticFiles cria um handler seguro para servir arquivos estáticos a partir
// de um diretório. O FileServer da biblioteca padrão normaliza caminhos e
// impede que .. escape do diretório raiz.
type StaticFiles struct {
	fileServer http.Handler
}

// NewStaticFiles cria um handler de arquivos estáticos.
func NewStaticFiles(directory string) http.Handler {
	return &StaticFiles{fileServer: http.FileServer(http.Dir(directory))}
}

// ServeHTTP implementa http.Handler.
func (s *StaticFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.fileServer == nil {
		http.NotFound(w, r)
		return
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/" + strings.TrimLeft(u.Path, "/")
	clone.URL = &u
	s.fileServer.ServeHTTP(w, clone)
}
