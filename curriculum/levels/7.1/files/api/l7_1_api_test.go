package api_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"burrow/api"
	"burrow/store"
)

func newAPI(t *testing.T) (string, *store.Memory) {
	t.Helper()
	st := store.NewMemory()
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer((&api.Server{Store: st}).Handler())
	t.Cleanup(srv.Close)
	return srv.URL, st
}

func do(t *testing.T, method, url, body string, headers ...string) (*http.Response, string) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestL7_1_KVRoundTrip(t *testing.T) {
	base, _ := newAPI(t)
	url := base + "/v1/kv/greeting"
	if resp, _ := do(t, "GET", url, ""); resp.StatusCode != 404 {
		t.Errorf("GET a missing key → %d, want 404", resp.StatusCode)
	}
	resp, _ := do(t, "PUT", url, "hello\x00world")
	if resp.StatusCode != 204 || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("PUT → %d ETag %q, want 204 ETag \"1\"", resp.StatusCode, resp.Header.Get("ETag"))
	}
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != 200 || body != "hello\x00world" {
		t.Errorf("GET → %d %q", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("GET headers: Content-Type %q ETag %q", resp.Header.Get("Content-Type"), resp.Header.Get("ETag"))
	}
	if resp.Header.Get("Expires") != "" {
		t.Errorf("no Expires header for a key without TTL")
	}
	if resp, _ := do(t, "PUT", base+"/v1/kv/empty", ""); resp.StatusCode != 204 {
		t.Errorf("PUT with an empty body stores an empty value: %d", resp.StatusCode)
	}
	if resp, body := do(t, "GET", base+"/v1/kv/empty", ""); resp.StatusCode != 200 || body != "" {
		t.Errorf("GET of an empty value → %d %q", resp.StatusCode, body)
	}
	if resp, _ := do(t, "DELETE", url, ""); resp.StatusCode != 204 {
		t.Errorf("DELETE → %d, want 204", resp.StatusCode)
	}
	if resp, _ := do(t, "DELETE", url, ""); resp.StatusCode != 404 {
		t.Errorf("DELETE again → %d, want 404", resp.StatusCode)
	}
	if resp, body := do(t, "PUT", base+"/v1/kv/"+strings.Repeat("k", 300), "v"); resp.StatusCode != 400 || !strings.Contains(body, "invalid key") {
		t.Errorf("an invalid key → %d %q, want 400 mentioning \"invalid key\"", resp.StatusCode, body)
	}
}

func TestL7_1_Keys(t *testing.T) {
	base, st := newAPI(t)
	for _, k := range []string{"user:2", "order:1", "user:1"} {
		st.Set(t.Context(), k, []byte("v"))
	}
	if _, body := do(t, "GET", base+"/v1/keys", ""); body != "order:1\nuser:1\nuser:2\n" {
		t.Errorf("GET /v1/keys = %q", body)
	}
	if _, body := do(t, "GET", base+"/v1/keys?prefix=user:", ""); body != "user:1\nuser:2\n" {
		t.Errorf("GET /v1/keys?prefix=user: = %q", body)
	}
}

func TestL7_1_IfMatch(t *testing.T) {
	base, st := newAPI(t)
	url := base + "/v1/kv/k"
	do(t, "PUT", url, "one")
	resp, _ := do(t, "PUT", url, "two", "If-Match", `"1"`)
	if resp.StatusCode != 204 || resp.Header.Get("ETag") != `"2"` {
		t.Errorf("PUT with the right If-Match → %d ETag %q, want 204 ETag \"2\"", resp.StatusCode, resp.Header.Get("ETag"))
	}
	if resp, _ := do(t, "PUT", url, "three", "If-Match", `"1"`); resp.StatusCode != 412 {
		t.Errorf("PUT with a stale If-Match → %d, want 412 Precondition Failed", resp.StatusCode)
	}
	if e, _ := st.Get(t.Context(), "k"); string(e.Value) != "two" {
		t.Errorf("a failed conditional PUT must not change the value: %q", e.Value)
	}
	if resp, _ := do(t, "PUT", url, "x", "If-Match", `"0"`); resp.StatusCode != 412 {
		t.Errorf("If-Match \"0\" means create only; the key exists → %d, want 412", resp.StatusCode)
	}
	if resp, _ := do(t, "PUT", base+"/v1/kv/fresh", "x", "If-Match", `"0"`); resp.StatusCode != 204 {
		t.Errorf("If-Match \"0\" on a missing key creates it → %d, want 204", resp.StatusCode)
	}
	if resp, _ := do(t, "PUT", base+"/v1/kv/ghost", "x", "If-Match", `"5"`); resp.StatusCode != 412 {
		t.Errorf("a conditional PUT on a missing key fails its precondition → %d, want 412", resp.StatusCode)
	}
	for _, bad := range []string{`*`, `W/"2"`, `2`, `"two"`, `""`} {
		if resp, _ := do(t, "PUT", url, "x", "If-Match", bad); resp.StatusCode != 400 {
			t.Errorf("If-Match %s isn't a quoted version → %d, want 400", bad, resp.StatusCode)
		}
	}
	if resp, _ := do(t, "DELETE", url, "", "If-Match", `"1"`); resp.StatusCode != 412 {
		t.Errorf("DELETE with a stale If-Match → %d, want 412", resp.StatusCode)
	}
	if resp, _ := do(t, "DELETE", url, "", "If-Match", `"2"`); resp.StatusCode != 204 {
		t.Errorf("DELETE with the right If-Match → %d, want 204", resp.StatusCode)
	}
	if resp, _ := do(t, "DELETE", url, "", "If-Match", `"2"`); resp.StatusCode != 412 {
		t.Errorf("a conditional DELETE of a missing key fails its precondition → %d, want 412", resp.StatusCode)
	}
	if resp, _ := do(t, "DELETE", url, ""); resp.StatusCode != 404 {
		t.Errorf("an unconditional DELETE of a missing key → %d, want 404", resp.StatusCode)
	}
}

func TestL7_1_TTL(t *testing.T) {
	base, _ := newAPI(t)
	url := base + "/v1/kv/session"
	if resp, _ := do(t, "PUT", url+"?ttl=50ms", "v"); resp.StatusCode != 204 {
		t.Fatalf("PUT ?ttl=50ms → %d", resp.StatusCode)
	}
	resp, _ := do(t, "GET", url, "")
	exp, err := http.ParseTime(resp.Header.Get("Expires"))
	if err != nil || time.Until(exp) < -time.Second {
		t.Errorf("GET of a key with a TTL carries Expires in http.TimeFormat: %q (%v)", resp.Header.Get("Expires"), err)
	}
	time.Sleep(100 * time.Millisecond)
	if resp, _ := do(t, "GET", url, ""); resp.StatusCode != 404 {
		t.Errorf("after the TTL the key is gone: %d", resp.StatusCode)
	}
	for _, q := range []string{"0", "-1s", "soon", "10"} {
		if resp, _ := do(t, "PUT", url+"?ttl="+q, "v"); resp.StatusCode != 400 {
			t.Errorf("?ttl=%s → %d, want 400", q, resp.StatusCode)
		}
	}
	resp, _ = do(t, "PUT", base+"/v1/kv/lock?ttl=1h", "me", "If-Match", `"0"`)
	if resp.StatusCode != 204 || resp.Header.Get("Expires") == "" {
		t.Errorf("If-Match \"0\" with ?ttl= creates with an expiry in one call (CompareAndSwapWithTTL): %d Expires %q",
			resp.StatusCode, resp.Header.Get("Expires"))
	}
	do(t, "PUT", base+"/v1/kv/short?ttl=80ms", "v")
	if resp, _ := do(t, "POST", base+"/v1/kv/short/touch?ttl=1h", ""); resp.StatusCode != 204 || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("POST touch → %d ETag %q, want 204 with the unchanged ETag", resp.StatusCode, resp.Header.Get("ETag"))
	}
	time.Sleep(150 * time.Millisecond)
	if resp, _ := do(t, "GET", base+"/v1/kv/short", ""); resp.StatusCode != 200 {
		t.Errorf("touch extends the TTL: GET after the old expiry → %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", base+"/v1/kv/missing/touch?ttl=1h", ""); resp.StatusCode != 404 {
		t.Errorf("touch of a missing key → %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", base+"/v1/kv/short/touch", ""); resp.StatusCode != 400 {
		t.Errorf("touch without ?ttl= → %d, want 400", resp.StatusCode)
	}
}

func TestL7_1_MethodsAndLimits(t *testing.T) {
	base, _ := newAPI(t)
	resp, _ := do(t, "PATCH", base+"/v1/kv/k", "x")
	if resp.StatusCode != 405 {
		t.Errorf("PATCH /v1/kv/k → %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") || !strings.Contains(allow, "DELETE") {
		t.Errorf("405 carries Allow listing GET, PUT and DELETE, got %q", allow)
	}
	big := bytes.Repeat([]byte("x"), 2<<20)
	if resp, _ := do(t, "PUT", base+"/v1/kv/big", string(big)); resp.StatusCode != 413 {
		t.Errorf("a 2 MiB PUT → %d, want 413 (http.MaxBytesReader)", resp.StatusCode)
	}
	if resp, _ := do(t, "PUT", base+"/v1/kv/fits", string(big[:1<<20])); resp.StatusCode != 204 {
		t.Errorf("exactly 1 MiB is allowed → %d", resp.StatusCode)
	}
}
