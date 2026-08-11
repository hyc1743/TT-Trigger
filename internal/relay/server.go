package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

const (
	maxBodyBytes      = 4096
	idempotencyTTL    = 10 * time.Minute
	signaturePreamble = "TT-TRIGGER-V1"
)

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
	RequestID string `json:"requestId"`
	Symbol    string `json:"symbol"`
	AddPair   bool   `json:"addPair,omitempty"`
}

type apiResponse struct {
	OK        bool   `json:"ok"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type storedResponse struct {
	status int
	body   apiResponse
}

type idempotencyEntry struct {
	fingerprint [32]byte
	done        chan struct{}
	response    storedResponse
	expires     time.Time
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
	cfg           Config
	logger        *log.Logger
	extensionHTTP *http.Server
	apiHTTP       []*http.Server
	upgrader      websocket.Upgrader
	hmacKeys      map[string][]byte

	mu        sync.Mutex
	extension *extensionConn
	pending   map[string]pendingTrigger
	busy      bool

	authMu sync.Mutex
	nonces map[string]time.Time

	idemMu      sync.Mutex
	idempotency map[string]*idempotencyEntry
}

func NewServer(cfg Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	keys := make(map[string][]byte, len(cfg.HMACKeys))
	for _, key := range cfg.HMACKeys {
		decoded, _ := base64.RawURLEncoding.DecodeString(key.Secret)
		keys[key.ID] = decoded
	}
	s := &Server{
		cfg:         cfg,
		logger:      logger,
		pending:     make(map[string]pendingTrigger),
		hmacKeys:    keys,
		nonces:      make(map[string]time.Time),
		idempotency: make(map[string]*idempotencyEntry),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(_ *http.Request) bool { return true },
		},
	}
	extensionMux := http.NewServeMux()
	extensionMux.HandleFunc("GET /health", s.handleHealth)
	extensionMux.HandleFunc("GET /extension", s.handleExtension)
	extensionMux.HandleFunc("/", s.handleNotFound)

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /health", s.handleAPIHealth)
	apiMux.HandleFunc("POST /webhook", s.handleWebhook)
	apiMux.HandleFunc("/", s.handleNotFound)

	s.extensionHTTP = newHTTPServer(cfg.ExtensionListen, s.securityHeaders(extensionMux))
	s.apiHTTP = append(s.apiHTTP, newHTTPServer(cfg.APIListen, s.securityHeaders(apiMux)))
	if cfg.AdditionalAPIListen != "" {
		s.apiHTTP = append(s.apiHTTP, newHTTPServer(cfg.AdditionalAPIListen, s.securityHeaders(apiMux)))
	}
	return s
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
}

func (s *Server) ExtensionHandler() http.Handler { return s.extensionHTTP.Handler }
func (s *Server) APIHandler() http.Handler       { return s.apiHTTP[0].Handler }

func (s *Server) ListenAndServe() error {
	extensionListener, err := net.Listen("tcp", s.extensionHTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen for extension on %s: %w", s.extensionHTTP.Addr, err)
	}
	apiListener, err := net.Listen("tcp", s.apiHTTP[0].Addr)
	if err != nil {
		_ = extensionListener.Close()
		return fmt.Errorf("listen for API on %s: %w", s.apiHTTP[0].Addr, err)
	}

	errCh := make(chan error, 2)
	s.logger.Printf("extension relay listening on %s", s.extensionHTTP.Addr)
	go func() { errCh <- s.extensionHTTP.Serve(extensionListener) }()
	s.logger.Printf("trigger API listening on %s", s.apiHTTP[0].Addr)
	go func() { errCh <- s.apiHTTP[0].Serve(apiListener) }()

	// The Tailscale listener is deliberately optional. Losing it must never take
	// down the loopback API or the extension relay.
	if len(s.apiHTTP) > 1 {
		optional := s.apiHTTP[1]
		listener, listenErr := net.Listen("tcp", optional.Addr)
		if listenErr != nil {
			s.logger.Printf("WARNING: Tailscale API unavailable on %s: %v", optional.Addr, listenErr)
		} else {
			s.logger.Printf("Tailscale trigger API listening on %s", optional.Addr)
			go func() {
				serveErr := optional.Serve(listener)
				if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					s.logger.Printf("WARNING: Tailscale API stopped: %v", serveErr)
				}
			}()
		}
	}

	err = <-errCh
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	_ = s.extensionHTTP.Close()
	for _, server := range s.apiHTTP {
		_ = server.Close()
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
	errs := []error{s.extensionHTTP.Shutdown(ctx)}
	for _, server := range s.apiHTTP {
		errs = append(errs, server.Shutdown(ctx))
	}
	return errors.Join(errs...)
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

func (s *Server) handleAPIHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found", "")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json", "")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		status, code := http.StatusBadRequest, "INVALID_JSON"
		if strings.Contains(err.Error(), "request body too large") {
			status, code = http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE"
		}
		writeAPIError(w, status, code, "request body must be a JSON object", "")
		return
	}

	keyID, authCode := s.authenticate(r, rawBody, time.Now())
	if authCode != "" {
		writeAPIError(w, http.StatusUnauthorized, authCode, "request authentication failed", "")
		return
	}

	decoder := json.NewDecoder(strings.NewReader(string(rawBody)))
	decoder.DisallowUnknownFields()
	var body triggerRequest
	if err := decoder.Decode(&body); err != nil || ensureJSONEOF(decoder) != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_JSON", "request body must contain one JSON object", "")
		return
	}
	if !validRequestID(body.RequestID) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST_ID", "requestId must be a UUID", "")
		return
	}
	symbol, ok := validateSymbol(body.Symbol)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "INVALID_SYMBOL", "symbol must contain between 1 and 256 characters", body.RequestID)
		return
	}
	body.Symbol = symbol

	fingerprint := sha256.Sum256([]byte(body.Symbol + "\x00" + strconv.FormatBool(body.AddPair)))
	idemKey := keyID + "\x00" + body.RequestID
	entry, owner, conflict := s.beginIdempotent(idemKey, fingerprint, time.Now())
	if conflict {
		writeAPIError(w, http.StatusConflict, "REQUEST_ID_CONFLICT", "requestId was already used with different parameters", body.RequestID)
		return
	}
	if !owner {
		select {
		case <-entry.done:
			writeJSON(w, entry.response.status, entry.response.body)
		case <-r.Context().Done():
		}
		return
	}

	response, cacheable := s.executeTrigger(body.RequestID, body.Symbol, body.AddPair)
	s.finishIdempotent(idemKey, entry, response, cacheable)
	writeJSON(w, response.status, response.body)
}

func (s *Server) authenticate(r *http.Request, rawBody []byte, now time.Time) (string, string) {
	keyID, keyIDOK := singleHeader(r.Header, "X-TT-Key-Id")
	timestampText, timestampOK := singleHeader(r.Header, "X-TT-Timestamp")
	nonceText, nonceOK := singleHeader(r.Header, "X-TT-Nonce")
	signatureText, signatureOK := singleHeader(r.Header, "X-TT-Signature")
	keyID = strings.TrimSpace(keyID)
	timestampText = strings.TrimSpace(timestampText)
	nonceText = strings.TrimSpace(nonceText)
	signatureText = strings.TrimSpace(signatureText)
	key, exists := s.hmacKeys[keyID]
	if !keyIDOK || !timestampOK || !nonceOK || !signatureOK || !exists || timestampText == "" || nonceText == "" || signatureText == "" {
		return "", "INVALID_SIGNATURE"
	}
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return "", "INVALID_SIGNATURE"
	}
	skew := time.Duration(s.cfg.SignatureMaxSkewSeconds) * time.Second
	requestTime := time.Unix(timestamp, 0)
	if requestTime.Before(now.Add(-skew)) || requestTime.After(now.Add(skew)) {
		return "", "STALE_REQUEST"
	}
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil || len(nonce) < 16 || len(nonce) > 64 {
		return "", "INVALID_SIGNATURE"
	}
	provided, err := hex.DecodeString(signatureText)
	if err != nil || len(provided) != sha256.Size || signatureText != strings.ToLower(signatureText) {
		return "", "INVALID_SIGNATURE"
	}
	bodyHash := sha256.Sum256(rawBody)
	canonical := signaturePreamble + "\nPOST\n/webhook\n" + timestampText + "\n" + nonceText + "\n" + hex.EncodeToString(bodyHash[:])
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(provided, expected) != 1 {
		return "", "INVALID_SIGNATURE"
	}

	nonceKey := keyID + "\x00" + nonceText
	// Keep every accepted nonce for the full possible acceptance interval. Using
	// requestTime+skew alone would immediately expire a request received at the
	// oldest allowed timestamp boundary.
	expires := now.Add(2 * skew)
	s.authMu.Lock()
	for stored, expiry := range s.nonces {
		if !expiry.After(now) {
			delete(s.nonces, stored)
		}
	}
	if _, replayed := s.nonces[nonceKey]; replayed {
		s.authMu.Unlock()
		return "", "REPLAYED_REQUEST"
	}
	s.nonces[nonceKey] = expires
	s.authMu.Unlock()
	return keyID, ""
}

func singleHeader(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func (s *Server) beginIdempotent(key string, fingerprint [32]byte, now time.Time) (*idempotencyEntry, bool, bool) {
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	for stored, entry := range s.idempotency {
		if !entry.expires.After(now) {
			delete(s.idempotency, stored)
		}
	}
	if entry, ok := s.idempotency[key]; ok {
		if entry.fingerprint != fingerprint {
			return nil, false, true
		}
		return entry, false, false
	}
	entry := &idempotencyEntry{fingerprint: fingerprint, done: make(chan struct{}), expires: now.Add(idempotencyTTL)}
	s.idempotency[key] = entry
	return entry, true, false
}

func (s *Server) finishIdempotent(key string, entry *idempotencyEntry, response storedResponse, cacheable bool) {
	s.idemMu.Lock()
	entry.response = response
	if !cacheable {
		delete(s.idempotency, key)
	}
	close(entry.done)
	s.idemMu.Unlock()
}

func (s *Server) executeTrigger(id, symbol string, addPair bool) (storedResponse, bool) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return apiError(http.StatusConflict, "TRIGGER_BUSY", "another trigger is currently running", id), false
	}
	conn := s.extension
	if conn == nil {
		s.mu.Unlock()
		return apiError(http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension is not connected", id), false
	}
	pending := pendingTrigger{conn: conn, result: make(chan wireMessage, 1)}
	s.pending[id] = pending
	s.busy = true
	s.mu.Unlock()

	defer s.clearPending(id)
	if err := conn.writeJSON(wireMessage{Type: "trigger", ID: id, Symbol: symbol, AddPair: addPair}); err != nil {
		conn.close(websocket.CloseInternalServerErr, "write failed")
		return apiError(http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension disconnected", id), true
	}

	timer := time.NewTimer(time.Duration(s.cfg.TriggerTimeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-pending.result:
		if result.OK {
			s.logger.Printf("trigger %s completed", id)
			return storedResponse{status: http.StatusOK, body: apiResponse{OK: true, RequestID: id}}, true
		}
		status := statusForResult(result.Code)
		message := result.Message
		if message == "" {
			message = "page action failed"
		}
		s.logger.Printf("trigger %s failed: %s", id, result.Code)
		return apiError(status, result.Code, message, id), true
	case <-conn.done:
		return apiError(http.StatusServiceUnavailable, "EXTENSION_OFFLINE", "Chrome extension disconnected", id), true
	case <-timer.C:
		return apiError(http.StatusGatewayTimeout, "EXTENSION_TIMEOUT", "Chrome extension did not respond in time", id), true
	}
}

func validateSymbol(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 256 {
		return "", false
	}
	return value, true
}

func validRequestID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func (s *Server) handleExtension(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4096)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth wireMessage
	if err := conn.ReadJSON(&auth); err != nil || auth.Type != "auth" || !s.extensionTokenEqual(auth.Token) {
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

func (s *Server) extensionTokenEqual(candidate string) bool {
	if len(candidate) != len(s.cfg.ExtensionToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.cfg.ExtensionToken)) == 1
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

func apiError(status int, code, message, requestID string) storedResponse {
	return storedResponse{status: status, body: apiResponse{OK: false, Code: code, Message: message, RequestID: requestID}}
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
	addresses := []string{s.cfg.APIListen}
	if s.cfg.AdditionalAPIListen != "" {
		addresses = append(addresses, s.cfg.AdditionalAPIListen)
	}
	return fmt.Sprintf("TT-Trigger(extension=%s, api=%s)", s.cfg.ExtensionListen, strings.Join(addresses, ","))
}
