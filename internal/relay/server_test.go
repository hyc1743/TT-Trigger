package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testToken = "01234567890123456789012345678901"

func newTestServer(t *testing.T, timeoutMS int) (*Server, *httptest.Server) {
	t.Helper()
	s := NewServer(Config{Listen: "127.0.0.1:0", Token: testToken, TriggerTimeoutMS: timeoutMS}, nil)
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)
	return s, httpServer
}

func request(t *testing.T, method, url, token, body string) (*http.Response, apiResponse) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded apiResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode response %q: %v", string(data), err)
	}
	return resp, decoded
}

func connectExtension(t *testing.T, baseURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/extension"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.WriteJSON(wireMessage{Type: "auth", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	var auth wireMessage
	if err := conn.ReadJSON(&auth); err != nil {
		t.Fatal(err)
	}
	if !auth.OK || auth.Type != "auth_result" {
		t.Fatalf("authentication rejected: %#v", auth)
	}
	return conn
}

func TestWebhookRejectsUnauthorizedAndInvalidInput(t *testing.T) {
	_, ts := newTestServer(t, 1000)

	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", "", `{"symbol":"BTC"}`)
	if resp.StatusCode != http.StatusUnauthorized || result.Code != "UNAUTHORIZED" {
		t.Fatalf("unexpected auth response: %d %#v", resp.StatusCode, result)
	}

	resp, result = request(t, http.MethodPost, ts.URL+"/webhook", testToken, `{"symbol":"  "}`)
	if resp.StatusCode != http.StatusBadRequest || result.Code != "INVALID_SYMBOL" {
		t.Fatalf("unexpected validation response: %d %#v", resp.StatusCode, result)
	}
}

func TestWebhookReportsExtensionOffline(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", testToken, `{"symbol":"BTC"}`)
	if resp.StatusCode != http.StatusServiceUnavailable || result.Code != "EXTENSION_OFFLINE" {
		t.Fatalf("unexpected response: %d %#v", resp.StatusCode, result)
	}
}

func TestWebhookRoundTrip(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.URL)

	done := make(chan struct{})
	go func() {
		defer close(done)
		var trigger wireMessage
		if err := conn.ReadJSON(&trigger); err != nil {
			return
		}
		_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, OK: true})
	}()

	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", testToken, `{"symbol":"BTC"}`)
	if resp.StatusCode != http.StatusOK || !result.OK || result.RequestID == "" {
		t.Fatalf("unexpected response: %d %#v", resp.StatusCode, result)
	}
	<-done
}

func TestWebhookMapsPageFailure(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.URL)

	go func() {
		var trigger wireMessage
		if conn.ReadJSON(&trigger) == nil {
			_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, Code: "INPUT_NOT_FOUND", Message: "Target input was not found"})
		}
	}()

	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", testToken, `{"symbol":"BTC"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity || result.Code != "INPUT_NOT_FOUND" {
		t.Fatalf("unexpected response: %d %#v", resp.StatusCode, result)
	}
}

func TestWebhookTimeout(t *testing.T) {
	_, ts := newTestServer(t, 250)
	conn := connectExtension(t, ts.URL)
	go func() {
		var ignored wireMessage
		_ = conn.ReadJSON(&ignored)
	}()

	start := time.Now()
	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", testToken, `{"symbol":"BTC"}`)
	if resp.StatusCode != http.StatusGatewayTimeout || result.Code != "EXTENSION_TIMEOUT" {
		t.Fatalf("unexpected response: %d %#v", resp.StatusCode, result)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatal("timeout returned too early")
	}
}

func TestBodyLimit(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	body, _ := json.Marshal(triggerRequest{Symbol: string(bytes.Repeat([]byte("x"), maxBodyBytes+1))})
	resp, result := request(t, http.MethodPost, ts.URL+"/webhook", testToken, string(body))
	if resp.StatusCode != http.StatusRequestEntityTooLarge || result.Code != "BODY_TOO_LARGE" {
		t.Fatalf("unexpected response: %d %#v", resp.StatusCode, result)
	}
}

func TestURLTriggerRoundTrip(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.URL)

	seenSymbol := make(chan string, 1)
	go func() {
		var trigger wireMessage
		if conn.ReadJSON(&trigger) == nil {
			seenSymbol <- trigger.Symbol
			_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, OK: true})
		}
	}()

	resp, result := request(t, http.MethodGet, ts.URL+"/trigger?token="+testToken+"&symbol=BTC", "", "")
	if resp.StatusCode != http.StatusOK || !result.OK {
		t.Fatalf("unexpected URL trigger response: %d %#v", resp.StatusCode, result)
	}
	if symbol := <-seenSymbol; symbol != "BTC" {
		t.Fatalf("unexpected symbol: %q", symbol)
	}
}

func TestURLTriggerPassesOptionalAddPair(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.URL)

	seenAddPair := make(chan bool, 1)
	go func() {
		var trigger wireMessage
		if conn.ReadJSON(&trigger) == nil {
			seenAddPair <- trigger.AddPair
			_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, OK: true})
		}
	}()

	url := ts.URL + "/trigger?token=" + testToken + "&symbol=BTC&addPair=true"
	resp, result := request(t, http.MethodGet, url, "", "")
	if resp.StatusCode != http.StatusOK || !result.OK {
		t.Fatalf("unexpected URL trigger response: %d %#v", resp.StatusCode, result)
	}
	if addPair := <-seenAddPair; !addPair {
		t.Fatal("addPair was not passed to the extension")
	}
}

func TestURLTriggerRejectsInvalidTokenAndSymbol(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	resp, result := request(t, http.MethodGet, ts.URL+"/trigger?token=wrong&symbol=BTC", "", "")
	if resp.StatusCode != http.StatusUnauthorized || result.Code != "UNAUTHORIZED" {
		t.Fatalf("unexpected token response: %d %#v", resp.StatusCode, result)
	}

	resp, result = request(t, http.MethodGet, ts.URL+"/trigger?token="+testToken, "", "")
	if resp.StatusCode != http.StatusBadRequest || result.Code != "INVALID_SYMBOL" {
		t.Fatalf("unexpected symbol response: %d %#v", resp.StatusCode, result)
	}

	resp, result = request(t, http.MethodGet, ts.URL+"/trigger?token="+testToken+"&symbol=BTC&addPair=maybe", "", "")
	if resp.StatusCode != http.StatusBadRequest || result.Code != "INVALID_ADD_PAIR" {
		t.Fatalf("unexpected addPair response: %d %#v", resp.StatusCode, result)
	}
}
