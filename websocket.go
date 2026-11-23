package gofast

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WebSocketConn representa uma conexão WebSocket.
type WebSocketConn struct {
	conn           net.Conn
	rw             *bufio.ReadWriter
	readMu         sync.Mutex
	writeMu        sync.Mutex
	closeMu        sync.Mutex
	closed         bool
	maxMessageSize int64
}

// WebSocketDisconnect representa um erro de desconexão WebSocket.
type WebSocketDisconnect struct {
	Code   int
	Reason string
}

func (e *WebSocketDisconnect) Error() string {
	return fmt.Sprintf("websocket disconnect: code=%d reason=%s", e.Code, e.Reason)
}

// WebSocketHandler é o tipo de handler para rotas WebSocket.
type WebSocketHandler func(conn *WebSocketConn) error

// ErrWebSocketOriginRejected indica que o cabeçalho Origin da requisição não
// foi aceito pela política de origem configurada.
var ErrWebSocketOriginRejected = errors.New("websocket origin rejected")

// WebSocketOptions configura o upgrade de uma conexão WebSocket.
type WebSocketOptions struct {
	// CheckOrigin valida a origem da requisição antes do upgrade. Quando nil,
	// aplica SameOriginWebSocketCheck (same-origin ou sem cabeçalho Origin).
	CheckOrigin func(*http.Request) bool
	// ReadLimit define o tamanho máximo de uma mensagem recebida, em bytes.
	// Zero usa o padrão de 1 MiB.
	ReadLimit int64
}

// SameOriginWebSocketCheck é a política de origem padrão: aceita clientes que
// não enviam Origin (CLI, testes, integrações servidor-a-servidor) e aceita
// navegadores apenas quando o Origin corresponde ao Host da requisição.
// Isso impede Cross-Site WebSocket Hijacking a partir de outra origem.
func SameOriginWebSocketCheck(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Cliente não-browser não envia Origin; o navegador sempre envia.
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// AllowAllWebSocketOrigins desabilita a validação de origem. Use apenas quando
// a autenticação do WebSocket não depende de cookies/credenciais implícitas.
func AllowAllWebSocketOrigins(*http.Request) bool { return true }

// UpgradeWebSocket atualiza uma conexão HTTP para WebSocket usando a política
// de origem padrão (same-origin).
func UpgradeWebSocket(w http.ResponseWriter, r *http.Request) (*WebSocketConn, error) {
	return UpgradeWebSocketWithOptions(w, r, WebSocketOptions{})
}

// UpgradeWebSocketWithOptions atualiza uma conexão HTTP para WebSocket.
func UpgradeWebSocketWithOptions(w http.ResponseWriter, r *http.Request, opts WebSocketOptions) (*WebSocketConn, error) {
	checkOrigin := opts.CheckOrigin
	if checkOrigin == nil {
		checkOrigin = SameOriginWebSocketCheck
	}
	if !checkOrigin(r) {
		return nil, ErrWebSocketOriginRejected
	}
	// Verificar se é upgrade de WebSocket
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("not a websocket request")
	}
	if r.Method != http.MethodGet {
		return nil, errors.New("websocket upgrade requires GET")
	}
	if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		return nil, errors.New("not an upgrade request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("unsupported WebSocket version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	decodedKey, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decodedKey) != 16 {
		return nil, errors.New("invalid Sec-WebSocket-Key header")
	}

	// Hijack a conexão
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("http.Hijacker not supported")
	}

	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("failed to hijack connection: %w", err)
	}

	// Enviar resposta de upgrade WebSocket
	acceptKey := computeWebSocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	response := fmt.Sprintf(
		"HTTP/1.1 101 Switching Protocols\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Accept: %s\r\n\r\n",
		acceptKey,
	)

	if _, err := buf.WriteString(response); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write upgrade response: %w", err)
	}
	if err := buf.Flush(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to flush upgrade response: %w", err)
	}

	readLimit := opts.ReadLimit
	if readLimit <= 0 {
		readLimit = 1 << 20
	}
	return &WebSocketConn{
		conn:           conn,
		rw:             bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn)),
		maxMessageSize: readLimit,
	}, nil
}

// computeWebSocketAccept calcula a chave de aceitação WebSocket.
func computeWebSocketAccept(key string) string {
	const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.Sum([]byte(key + magicGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// ReadMessage lê uma mensagem WebSocket.
func (c *WebSocketConn) ReadMessage() (messageType int, data []byte, err error) {
	return c.readFrame()
}

// WriteMessage escreve uma mensagem WebSocket.
func (c *WebSocketConn) WriteMessage(messageType int, data []byte) error {
	return c.writeFrame(messageType, data)
}

// ReadJSON lê uma mensagem JSON do WebSocket.
func (c *WebSocketConn) ReadJSON(v any) error {
	_, data, err := c.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// WriteJSON escreve uma mensagem JSON no WebSocket.
func (c *WebSocketConn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(1, data) // 1 = text frame
}

// ReadText lê uma mensagem de texto.
func (c *WebSocketConn) ReadText() (string, error) {
	_, data, err := c.ReadMessage()
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteText escreve uma mensagem de texto.
func (c *WebSocketConn) WriteText(text string) error {
	return c.WriteMessage(1, []byte(text))
}

// ReadBinary lê uma mensagem binária.
func (c *WebSocketConn) ReadBinary() ([]byte, error) {
	_, data, err := c.ReadMessage()
	if err != nil {
		return nil, err
	}
	return data, nil
}

// WriteBinary escreve uma mensagem binária.
func (c *WebSocketConn) WriteBinary(data []byte) error {
	return c.WriteMessage(2, data) // 2 = binary frame
}

// Close fecha a conexão WebSocket.
func (c *WebSocketConn) Close() error {
	c.closeMu.Lock()
	if c.closed {
		c.closeMu.Unlock()
		return nil
	}
	c.closed = true
	c.closeMu.Unlock()
	_ = c.writeFrame(8, []byte{0x03, 0xe8}) // 1000: normal closure
	return c.conn.Close()
}

// SetReadLimit configura o tamanho máximo aceito para uma mensagem.
func (c *WebSocketConn) SetReadLimit(maxBytes int64) {
	if maxBytes > 0 {
		c.maxMessageSize = maxBytes
	}
}

// SetDeadline define o prazo para leitura/escrita.
func (c *WebSocketConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline define o prazo para leitura.
func (c *WebSocketConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline define o prazo para escrita.
func (c *WebSocketConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// readFrame lê um frame WebSocket.
func (c *WebSocketConn) readFrame() (int, []byte, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	for {
		var header [2]byte
		if _, err := io.ReadFull(c.rw, header[:]); err != nil {
			return 0, nil, err
		}
		fin := header[0]&0x80 != 0
		opcode := int(header[0] & 0x0f)
		masked := header[1]&0x80 != 0
		length := uint64(header[1] & 0x7f)
		if !fin {
			return 0, nil, errors.New("fragmented WebSocket frames are not supported")
		}
		if !masked {
			return 0, nil, errors.New("client WebSocket frame must be masked")
		}
		if length == 126 {
			var ext [2]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return 0, nil, err
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		} else if length == 127 {
			var ext [8]byte
			if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
				return 0, nil, err
			}
			if ext[0]&0x80 != 0 {
				return 0, nil, errors.New("invalid WebSocket payload length")
			}
			length = binary.BigEndian.Uint64(ext[:])
		}
		if c.maxMessageSize > 0 && length > uint64(c.maxMessageSize) {
			return 0, nil, errors.New("WebSocket message exceeds configured limit")
		}
		if opcode >= 8 && (!fin || length > 125) {
			return 0, nil, errors.New("invalid WebSocket control frame")
		}
		var mask [4]byte
		if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
			return 0, nil, err
		}
		data := make([]byte, int(length))
		if length > 0 {
			if _, err := io.ReadFull(c.rw, data); err != nil {
				return 0, nil, err
			}
			for i := range data {
				data[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 8:
			return 8, data, io.EOF
		case 9:
			if err := c.writeFrame(10, data); err != nil {
				return 0, nil, err
			}
			continue
		case 10:
			continue
		case 1, 2:
			return opcode, data, nil
		default:
			return 0, nil, fmt.Errorf("unsupported WebSocket opcode %d", opcode)
		}
	}
}

func (c *WebSocketConn) writeFrame(opcode int, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var header [10]byte
	header[0] = 0x80 | byte(opcode)
	length := len(data)
	headerLen := 2
	header[1] = byte(length)
	if length >= 126 && length <= 0xffff {
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
		headerLen = 4
	} else if length > 0xffff {
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
		headerLen = 10
	}
	if _, err := c.rw.Write(header[:headerLen]); err != nil {
		return err
	}
	if length > 0 {
		if _, err := c.rw.Write(data); err != nil {
			return err
		}
	}
	return c.rw.Flush()
}
