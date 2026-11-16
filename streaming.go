package gofast

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Event representa um evento Server-Sent Events (SSE).
type Event struct {
	ID    string
	Event string
	Data  string
	Retry int
}

// SSEResponse representa uma resposta Server-Sent Events.
type SSEResponse struct {
	stream func(io.Writer) error
}

// SSE cria uma resposta SSE a partir de uma função de stream.
func SSE(stream func(io.Writer) error) SSEResponse {
	return SSEResponse{stream: stream}
}

// Render renderiza a resposta SSE escrevendo no ResponseWriter.
func (s SSEResponse) Render(w io.Writer) error {
	if s.stream != nil {
		return s.stream(w)
	}
	return nil
}

// WriteEvent escreve um único evento SSE no writer.
func WriteEvent(w io.Writer, event Event) error {
	if event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", event.ID)
	}
	if event.Event != "" {
		fmt.Fprintf(w, "event: %s\n", event.Event)
	}
	if event.Data != "" {
		// SSE data pode ter múltiplas linhas
		for _, line := range splitLines(event.Data) {
			fmt.Fprintf(w, "data: %s\n", line)
		}
	}
	if event.Retry > 0 {
		fmt.Fprintf(w, "retry: %d\n", event.Retry)
	}
	fmt.Fprintf(w, "\n")
	return nil
}

// splitLines divide uma string em linhas preservando o conteúdo.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// StreamingResponse representa uma resposta de streaming genérica.
type StreamingResponse struct {
	stream      func(io.Writer) error
	contentType string
}

// Stream cria uma StreamingResponse a partir de uma função de stream.
func Stream(stream func(io.Writer) error) StreamingResponse {
	return StreamingResponse{
		stream:      stream,
		contentType: "text/event-stream",
	}
}

// StreamWithContentType cria uma StreamingResponse com content-type customizado.
func StreamWithContentType(stream func(io.Writer) error, contentType string) StreamingResponse {
	return StreamingResponse{
		stream:      stream,
		contentType: contentType,
	}
}

// Render renderiza a resposta de streaming.
func (s StreamingResponse) Render(w io.Writer) error {
	if s.stream != nil {
		return s.stream(w)
	}
	return nil
}

// HeartbeatConfig configura o heartbeat para SSE.
type HeartbeatConfig struct {
	Interval time.Duration
	Message  string
}

// SSEWithHeartbeat cria uma resposta SSE com heartbeat automático.
type synchronizedStreamWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *synchronizedStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (w *synchronizedStreamWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if flusher, ok := w.w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// SSEWithHeartbeat envia heartbeats sem concorrência não sincronizada no
// ResponseWriter. O stream principal e o ticker compartilham um writer serial.
func SSEWithHeartbeat(stream func(io.Writer) error, config HeartbeatConfig) SSEResponse {
	return SSE(func(w io.Writer) error {
		if stream == nil {
			return nil
		}
		safeWriter := &synchronizedStreamWriter{w: w}
		if config.Interval <= 0 {
			return stream(safeWriter)
		}

		done := make(chan struct{})
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(config.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if config.Message != "" {
						_, _ = fmt.Fprintf(safeWriter, "data: %s\n\n", config.Message)
					} else {
						_, _ = io.WriteString(safeWriter, ":\n\n")
					}
					safeWriter.Flush()
				}
			}
		}()

		err := stream(safeWriter)
		close(done)
		<-heartbeatDone
		return err
	})
}

var _ = io.Copy
var _ = time.Second
