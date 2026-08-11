package relay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	testExtensionToken = "01234567890123456789012345678901"
	testRequestID      = "550e8400-e29b-41d4-a716-446655440000"
)

var testHMACSecret = []byte("abcdefghijklmnopqrstuvwxyzABCDEF")

type testServers struct{ URL, ExtensionURL string }

func newTestServer(t *testing.T, timeoutMS int) (*Server, *testServers) {
	t.Helper()
	s := NewServer(Config{
		ExtensionListen: "127.0.0.1:0", APIListen: "127.0.0.1:0",
		ExtensionToken:          testExtensionToken,
		HMACKeys:                []HMACKey{{ID: "test", Secret: base64.RawURLEncoding.EncodeToString(testHMACSecret)}},
		SignatureMaxSkewSeconds: 30, TriggerTimeoutMS: timeoutMS,
	}, nil)
	api := httptest.NewServer(s.APIHandler())
	ext := httptest.NewServer(s.ExtensionHandler())
	t.Cleanup(api.Close)
	t.Cleanup(ext.Close)
	return s, &testServers{URL: api.URL, ExtensionURL: ext.URL}
}

func signedRequest(t *testing.T, url, body, nonce string, at time.Time, mutateSig bool) (*http.Response, apiResponse) {
	t.Helper()
	timestamp := strconv.FormatInt(at.Unix(), 10)
	if nonce == "" {
		nonce = base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	}
	hash := sha256.Sum256([]byte(body))
	canonical := signaturePreamble + "\nPOST\n/webhook\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(hash[:])
	mac := hmac.New(sha256.New, testHMACSecret)
	_, _ = mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))
	if mutateSig {
		sig = strings.Repeat("0", 64)
	}
	req, err := http.NewRequest(http.MethodPost, url+"/webhook", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-TT-Key-Id", "test")
	req.Header.Set("X-TT-Timestamp", timestamp)
	req.Header.Set("X-TT-Nonce", nonce)
	req.Header.Set("X-TT-Signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var decoded apiResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode %q: %v", data, err)
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
	_ = conn.WriteJSON(wireMessage{Type: "auth", Token: testExtensionToken})
	var auth wireMessage
	if err := conn.ReadJSON(&auth); err != nil || !auth.OK {
		t.Fatalf("auth failed: %#v %v", auth, err)
	}
	return conn
}

func validBody() string { return `{"requestId":"` + testRequestID + `","symbol":"BTC","addPair":true}` }

func TestWebhookRejectsBadSignatureStaleAndReplay(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	resp, result := signedRequest(t, ts.URL, validBody(), "", time.Now(), true)
	if resp.StatusCode != 401 || result.Code != "INVALID_SIGNATURE" {
		t.Fatalf("bad signature accepted: %d %#v", resp.StatusCode, result)
	}
	resp, result = signedRequest(t, ts.URL, validBody(), "", time.Now().Add(-time.Minute), false)
	if resp.StatusCode != 401 || result.Code != "STALE_REQUEST" {
		t.Fatalf("stale accepted: %d %#v", resp.StatusCode, result)
	}
	nonce := base64.RawURLEncoding.EncodeToString([]byte("unique-nonce-1234"))
	resp, _ = signedRequest(t, ts.URL, validBody(), nonce, time.Now(), false)
	if resp.StatusCode != 503 {
		t.Fatalf("expected offline after valid auth: %d", resp.StatusCode)
	}
	resp, result = signedRequest(t, ts.URL, validBody(), nonce, time.Now(), false)
	if resp.StatusCode != 401 || result.Code != "REPLAYED_REQUEST" {
		t.Fatalf("replay accepted: %d %#v", resp.StatusCode, result)
	}
}

func TestWebhookRoundTripAndIdempotency(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.ExtensionURL)
	seen := make(chan wireMessage, 1)
	go func() {
		var trigger wireMessage
		if conn.ReadJSON(&trigger) == nil {
			seen <- trigger
			_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, OK: true})
		}
	}()
	resp, result := signedRequest(t, ts.URL, validBody(), base64.RawURLEncoding.EncodeToString([]byte("first-nonce-12345")), time.Now(), false)
	if resp.StatusCode != 200 || !result.OK || result.RequestID != testRequestID {
		t.Fatalf("round trip failed: %d %#v", resp.StatusCode, result)
	}
	trigger := <-seen
	if trigger.Symbol != "BTC" || !trigger.AddPair {
		t.Fatalf("wrong trigger: %#v", trigger)
	}
	resp, result = signedRequest(t, ts.URL, validBody(), base64.RawURLEncoding.EncodeToString([]byte("second-nonce-1234")), time.Now(), false)
	if resp.StatusCode != 200 || !result.OK {
		t.Fatalf("cached result failed: %d %#v", resp.StatusCode, result)
	}
}

func TestRequestIDConflict(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	conn := connectExtension(t, ts.ExtensionURL)
	go func() {
		var trigger wireMessage
		if conn.ReadJSON(&trigger) == nil {
			_ = conn.WriteJSON(wireMessage{Type: "result", ID: trigger.ID, OK: true})
		}
	}()
	resp, _ := signedRequest(t, ts.URL, validBody(), base64.RawURLEncoding.EncodeToString([]byte("first-conflict-12")), time.Now(), false)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	changed := strings.Replace(validBody(), "BTC", "ETH", 1)
	resp, result := signedRequest(t, ts.URL, changed, base64.RawURLEncoding.EncodeToString([]byte("other-conflict-12")), time.Now(), false)
	if resp.StatusCode != 409 || result.Code != "REQUEST_ID_CONFLICT" {
		t.Fatalf("conflict missed: %d %#v", resp.StatusCode, result)
	}
}

func TestLegacyEndpointsRemovedAndIsolation(t *testing.T) {
	_, ts := newTestServer(t, 1000)
	for _, url := range []string{ts.URL + "/trigger?token=x&symbol=BTC", ts.ExtensionURL + "/webhook"} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("legacy/leaked endpoint returned %d", resp.StatusCode)
		}
	}
}

func TestWebhookTimeout(t *testing.T) {
	_, ts := newTestServer(t, 250)
	conn := connectExtension(t, ts.ExtensionURL)
	go func() { var ignored wireMessage; _ = conn.ReadJSON(&ignored) }()
	resp, result := signedRequest(t, ts.URL, validBody(), base64.RawURLEncoding.EncodeToString([]byte("timeout-nonce-123")), time.Now(), false)
	if resp.StatusCode != 504 || result.Code != "EXTENSION_TIMEOUT" {
		t.Fatalf("timeout failed: %d %#v", resp.StatusCode, result)
	}
}
