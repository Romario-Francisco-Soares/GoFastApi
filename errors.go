package gofast

import (
	"fmt"
	"net/http"
)

// HTTPError representa um erro HTTP com código, mensagem e detalhes.
type HTTPError struct {
	Status  int
	Code    string
	Message string
	Headers map[string]string
	Details any
	Cause   error
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, e.Code, e.Message)
}

// ValidationError representa um erro de validação (equivalente a RequestValidationError).
type ValidationError struct {
	Errors []ValidationErrorDetail
}

type ValidationErrorDetail struct {
	Location []string `json:"loc"`
	Field    string   `json:"field"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Rejected any      `json:"rejected,omitempty"`
}

func (e *ValidationError) Error() string {
	return "validation error"
}

// WebSocketValidationError representa erro de validação WebSocket.
type WebSocketValidationError struct {
	Message string
}

func (e *WebSocketValidationError) Error() string {
	return e.Message
}

// NewHTTPError cria um HTTPError.
func NewHTTPError(status int, code, message string) *HTTPError {
	return &HTTPError{
		Status:  status,
		Code:    code,
		Message: message,
		Headers: make(map[string]string),
	}
}

// ensure http pacote é usado
var _ = http.StatusOK
