// Package testing fornece TestClient para testes de integração
// contra uma aplicação GoFastAPI em loopback, sem expor portas reais.
package testing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"

	gofast "github.com/Romario-Francisco-Soares/GoFastApi"
)

// TestClient é um cliente de teste que serve uma aplicação GoFastAPI
// em um servidor httptest em loopback, mantendo cookies entre requests.
type TestClient struct {
	server *httptest.Server
	client *http.Client
	base   string
}

// NewTestClient cria um TestClient para a aplicação informada.
// O servidor é iniciado imediatamente em uma porta aleatória de loopback.
func NewTestClient(app *gofast.App) *TestClient {
	jar, _ := cookiejar.New(nil)
	server := httptest.NewServer(app.Handler())
	return &TestClient{
		server: server,
		client: &http.Client{Jar: jar},
		base:   server.URL,
	}
}

// Close encerra o servidor de teste e libera recursos.
func (c *TestClient) Close() {
	c.server.Close()
}

// URL devolve a URL base do servidor de teste.
func (c *TestClient) URL() string {
	return c.base
}

// Get executa uma requisição GET no caminho informado.
func (c *TestClient) Get(path string) (*http.Response, error) {
	return c.client.Get(c.base + path)
}

// PostJSON executa uma requisição POST com body JSON e Content-Type application/json.
func (c *TestClient) PostJSON(path string, body []byte) (*http.Response, error) {
	return c.client.Post(c.base+path, "application/json", bytes.NewReader(body))
}

// PostForm executa uma requisição POST com application/x-www-form-urlencoded.
func (c *TestClient) PostForm(path string, data url.Values) (*http.Response, error) {
	return c.client.PostForm(c.base+path, data)
}

// PostMultipart executa uma requisição POST multipart/form-data.
// fields contém pares chave-valor de campos de formulário.
// files contém nomes de arquivo como chave e conteúdo como valor.
func (c *TestClient) PostMultipart(path string, fields map[string]string, files map[string][]byte) (*http.Response, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return nil, fmt.Errorf("write field %q: %w", k, err)
		}
	}

	for name, content := range files {
		part, err := writer.CreateFormFile(name, name)
		if err != nil {
			return nil, fmt.Errorf("create form file %q: %w", name, err)
		}
		if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
			return nil, fmt.Errorf("write file %q: %w", name, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("create multipart request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return c.client.Do(req)
}

// NewRequest cria um *http.Request apontando para o servidor de teste.
func (c *TestClient) NewRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	return req, nil
}

// Do executa qualquer *http.Request no cliente de teste.
func (c *TestClient) Do(req *http.Request) (*http.Response, error) {
	return c.client.Do(req)
}

// JSONResponse decodifica o corpo de uma resposta HTTP como JSON em um mapa.
func JSONResponse(resp *http.Response) (map[string]any, error) {
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return v, nil
}

// StatusOK verifica se a resposta tem status 200.
func StatusOK(resp *http.Response) bool {
	return resp.StatusCode == http.StatusOK
}

// Strings é um helper para evitar import não utilizado em builds futuros.
var _ = strings.TrimSpace
