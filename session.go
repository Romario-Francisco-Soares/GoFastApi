package gofast

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type sessionContextKey struct{}

// SessionConfig controla a sessão stateless armazenada em cookie AES-GCM.
type SessionConfig struct {
	Secret     string
	CookieName string
	Path       string
	Domain     string
	MaxAge     time.Duration
	Secure     bool
	HTTPOnly   bool
	SameSite   http.SameSite
}

// SessionMiddleware carrega e disponibiliza uma sessão criptografada por cookie.
// O segredo deve ser estável, aleatório e mantido fora do código-fonte.
func SessionMiddleware(cfg SessionConfig) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := newCookieSession(r, w, cfg)
			if err != nil {
				http.Error(w, `{"error":"session configuration invalid"}`, http.StatusInternalServerError)
				return
			}
			ctx := context.WithValue(r.Context(), sessionContextKey{}, session)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// newCookieSession validates the secret and decrypts an existing session cookie.
func newCookieSession(r *http.Request, w http.ResponseWriter, cfg SessionConfig) (*Session, error) {
	if len(cfg.Secret) < 16 {
		return nil, errors.New("session secret must contain at least 16 bytes")
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "gofast_session"
	}
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 24 * time.Hour
	}
	if !cfg.HTTPOnly {
		// Explicit false is supported; production callers should normally set true.
	}
	key := sha256.Sum256([]byte(cfg.Secret))
	s := &Session{req: r, w: w, data: make(map[string]any), cfg: cfg, key: key[:]}
	if cookie, err := r.Cookie(cfg.CookieName); err == nil && cookie.Value != "" {
		if err := s.decode(cookie.Value); err != nil {
			// Invalid or tampered cookie is ignored and replaced on the next write.
			s.data = make(map[string]any)
		}
	}
	return s, nil
}

func (s *Session) decode(value string) error {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	if len(encoded) < gcm.NonceSize() {
		return errors.New("session cookie too short")
	}
	nonce, ciphertext := encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(s.cfg.CookieName))
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, &s.data)
}

func (s *Session) saveCookie() error {
	if s == nil || s.w == nil || len(s.key) == 0 {
		return nil
	}
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	payload, err := json.Marshal(s.data)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("encode session: %w", err)
	}
	cfg := s.cfg
	key := append([]byte(nil), s.key...)
	s.dirty = false
	s.mu.Unlock()

	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nonce, nonce, payload, []byte(cfg.CookieName))
	value := base64.RawURLEncoding.EncodeToString(sealed)
	if len(value) > 3800 {
		return errors.New("session payload exceeds safe cookie size")
	}
	cookie := &http.Cookie{
		Name: cfg.CookieName, Value: value, Path: cfg.Path, Domain: cfg.Domain,
		MaxAge: int(cfg.MaxAge.Seconds()), Expires: time.Now().Add(cfg.MaxAge),
		Secure: cfg.Secure, HttpOnly: cfg.HTTPOnly, SameSite: cfg.SameSite,
	}
	setCookieReplacing(s.w.Header(), cookie)
	return nil
}

func setCookieReplacing(header http.Header, cookie *http.Cookie) {
	prefix := cookie.Name + "="
	values := header.Values("Set-Cookie")
	header.Del("Set-Cookie")
	for _, value := range values {
		if !strings.HasPrefix(value, prefix) {
			header.Add("Set-Cookie", value)
		}
	}
	header.Add("Set-Cookie", cookie.String())
}

// Clear remove a sessão e expira o cookie no cliente.
func (s *Session) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.data = make(map[string]any)
	s.dirty = false
	cfg, w := s.cfg, s.w
	s.mu.Unlock()
	if w != nil && cfg.CookieName != "" {
		setCookieReplacing(w.Header(), &http.Cookie{Name: cfg.CookieName, Value: "", Path: cfg.Path, Domain: cfg.Domain, MaxAge: -1, Expires: time.Unix(1, 0), Secure: cfg.Secure, HttpOnly: cfg.HTTPOnly, SameSite: cfg.SameSite})
	}
}
