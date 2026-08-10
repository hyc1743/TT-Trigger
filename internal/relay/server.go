package relay

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const maxBodyBytes = 4096

type wireMessage struct {
	Type    string `json:"type"`
	Token   string `json:"token,omitempty"`
	ID      string `json:"id,omitempty"`
	Symbol  string `json:"symbol,omitempty"`
	AddPair bool   `json:"addPair,omitempty"`
	OK      bool   `json:"ok,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type triggerRequest struct {
	Symbol  string `json:"symbol"`
	AddPair bool   `json:"addPair,omitempty"`
}

type apiResponse struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type pendingTrigger struct {
	conn   *extensionConn
	result chan wireMessage
}

type extensionConn struct {
	ws        *websocket.Conn
	writeMu   sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

func (c *extensionConn) writeJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.WriteJSON(value)
}

func (c *extensionConn) close(code int, reason string) {
	c.closeOnce.Do(func() {
		close(c.done)
		c.writeMu.Lock()
		defer c.writeMu.Unlock()
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		_ = c.ws.Close()
	})
}

type Server struct {
	cfg      Config
	logger   *log.Logger
	http     *http.Server
	upgrader websocket.Upgrader

	mu        sync.Mutex
	extension *extensionConn
	pending   map[string]pendingTrigger
	busy      bool
}

func NewServer(cfg Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &Server{
		cfg:     cfg,
		logger:  logger,
		pending: make(map[string]pendingTrigger),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /trigger", s.handleURLTrigger)
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("GET /extension", s.handleExtension)
	mux.HandleFunc("/", s.handleNotFound)
	s.http = &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

func (s *Server) ListenAndServe() error {
	s.logger.Printf("TT-Trigger listening on %s", s.cfg.Listen)
	err := s.http.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	conn := s.extension
	s.extension = nil
	s.mu.Unlock()
	if conn != nil {
		conn.close(websocket.CloseGoingAway, "server shutdown")
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found", "")
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	connected := s.extension != nil
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extensionConnected": connected})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing bearer token", "")
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body triggerRequest
	if err := decoder.Decode(&body); err != nil {
		status := http.StatusBadRequest
		code := "INVALID_JSON"
		if strings.Contains(err.Error(), "request body too large") {
			status = http.StatusRequestEntityTooLarge
			code = "BODY_TOO_LARGE"
		}
		writeAPIError(w, status, code, "request body must be a JSON object containing symbol", "")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_JSON", "request body must contain one JSON object", "")
		return
	}
	symbol, ok := validateSymbol(body.Symbol)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SYMBOL", "symbol must contain between 1 and 256 characters", "")
		return
	}
	s.executeTrigger(w, r, symbol, body.AddPair)
}

func (s *Server) handleURLTrigger(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !s.tokenEqual(strings.TrimSpace(query.Get("token"))) {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing token", "")
		return
	}
	symbolValues, exists := query["symbol"]
	if !exists || len(symbolValues) != 1 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SYMBOL", "URL must contain one symbol parameter", "")
		return
	}
	symbol, ok := validateSymbol(symbolValues[0])
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SYMBOL", "symbol must contain between 1 and 256 characters", "")
		return
	}
	addPairValues, addPairExists := query["addPair"]
	addPair, ok := parseOptionalBool(addPairValues, addPairExists)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "INVALID_ADD_PAIR", "addPair must be true, false, 1, or 0", "")
		return
	}
	s.executeTrigger(w, r, symbol, addPair)
}

func (s *Server) executeTrigger(w http.ResponseWriter, r *http.Request, symbol string, addPair bool) {

	id, err := requestID()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "could not create request", "")
		return
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		writeAPIError(w, http.StatusConflict, "TRIGGER_BUSY", "another trigger is currently running", id)
		return
	}
	conn := s.extension
	if conn == nil {
		s.mu.Unlock()
		writeAPIError(w, http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension is not connected", id)
		return
	}
	pending := pendingTrigger{conn: conn, result: make(chan wireMessage, 1)}
	s.pending[id] = pending
	s.busy = true
	s.mu.Unlock()

	defer s.clearPending(id)
	if err := conn.writeJSON(wireMessage{Type: "trigger", ID: id, Symbol: symbol, AddPair: addPair}); err != nil {
		conn.close(websocket.CloseInternalServerErr, "write failed")
		writeAPIError(w, http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension disconnected", id)
		return
	}

	timer := time.NewTimer(time.Duration(s.cfg.TriggerTimeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-pending.result:
		if result.OK {
			s.logger.Printf("trigger %s completed", id)
			writeJSON(w, http.StatusOK, apiResponse{OK: true, RequestID: id})
			return
		}
		status := statusForResult(result.Code)
		message := result.Message
		if message == "" {
			message = "page action failed"
		}
		s.logger.Printf("trigger %s failed: %s", id, result.Code)
		writeAPIError(w, status, result.Code, message, id)
	case <-conn.done:
		writeAPIError(w, http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension disconnected", id)
	case <-timer.C:
		writeAPIError(w, http.StatusGatewayTimeout, "EXTENSION_TIMEOUT", "Chrome extension did not respond in time", id)
	case <-r.Context().Done():
		return
	}
}

func validateSymbol(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 256 {
		return "", false
	}
	return value, true
}

func parseOptionalBool(values []string, exists bool) (bool, bool) {
	if !exists {
		return false, true
	}
	if len(values) != 1 {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(values[0])) {
	case "true", "1":
		return true, true
	case "false", "0", "":
		return false, true
	default:
		return false, false
	}
}

func (s *Server) handleExtension(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth wireMessage
	if err := conn.ReadJSON(&auth); err != nil || auth.Type != "auth" || !s.tokenEqual(auth.Token) {
		_ = conn.WriteJSON(wireMessage{Type: "auth_result", OK: false, Code: "UNAUTHORIZED"})
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	ext := &extensionConn{ws: conn, done: make(chan struct{})}
	s.mu.Lock()
	previous := s.extension
	s.extension = ext
	s.mu.Unlock()
	if previous != nil {
		previous.close(websocket.CloseNormalClosure, "replaced by new connection")
	}
	if err := ext.writeJSON(wireMessage{Type: "auth_result", OK: true}); err != nil {
		ext.close(websocket.CloseInternalServerErr, "authentication response failed")
		s.unregister(ext)
		return
	}
	s.logger.Printf("Chrome extension connected")
	s.readExtension(ext)
}

func (s *Server) readExtension(conn *extensionConn) {
	defer func() {
		conn.close(websocket.CloseNormalClosure, "connection closed")
		s.unregister(conn)
		s.logger.Printf("Chrome extension disconnected")
	}()
	for {
		var msg wireMessage
		if err := conn.ws.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "ping":
			if err := conn.writeJSON(wireMessage{Type: "pong"}); err != nil {
				return
			}
		case "result":
			s.dispatchResult(conn, msg)
		}
	}
}

func (s *Server) dispatchResult(conn *extensionConn, result wireMessage) {
	s.mu.Lock()
	pending, ok := s.pending[result.ID]
	s.mu.Unlock()
	if !ok || pending.conn != conn {
		return
	}
	select {
	case pending.result <- result:
	default:
	}
}

func (s *Server) unregister(conn *extensionConn) {
	s.mu.Lock()
	if s.extension == conn {
		s.extension = nil
	}
	s.mu.Unlock()
}

func (s *Server) clearPending(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.busy = false
	s.mu.Unlock()
}

func (s *Server) authorized(header string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return s.tokenEqual(strings.TrimSpace(strings.TrimPrefix(header, prefix)))
}

func (s *Server) tokenEqual(candidate string) bool {
	if len(candidate) != len(s.cfg.Token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.cfg.Token)) == 1
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

func requestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func statusForResult(code string) int {
	switch code {
	case "NO_TARGET_TAB":
		return http.StatusConflict
	case "INPUT_NOT_FOUND", "INPUT_NOT_INTERACTIVE", "ADD_PAIR_BUTTON_NOT_FOUND":
		return http.StatusUnprocessableEntity
	case "INVALID_MESSAGE":
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(w, status, apiResponse{OK: false, Code: code, Message: message, RequestID: requestID})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) String() string {
	return fmt.Sprintf("TT-Trigger(%s)", s.cfg.Listen)
}
