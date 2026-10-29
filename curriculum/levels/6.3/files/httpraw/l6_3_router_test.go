package httpraw_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"burrow/httpraw"
	"burrow/store"
)

func routed(t *testing.T, setup func(r *httpraw.Router)) string {
	t.Helper()
	r := httpraw.NewRouter()
	setup(r)
	return "http://" + serve(t, &httpraw.Server{Handler: r})
}

func say(s string) httpraw.HandlerFunc {
	return func(w *httpraw.ResponseWriter, r *httpraw.Request) { w.Write([]byte(s)) }
}

func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if body == "" {
		req.Body = nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestL6_3_MethodAndPath(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) {
		r.Handle("GET /things", say("list"))
		r.Handle("POST /things", say("create"))
		r.HandleFunc("DELETE /things", func(w *httpraw.ResponseWriter, _ *httpraw.Request) { w.WriteHeader(204) })
	})
	if _, b := do(t, "GET", base+"/things", ""); b != "list" {
		t.Errorf("GET /things → %q", b)
	}
	if _, b := do(t, "POST", base+"/things", "x"); b != "create" {
		t.Errorf("POST /things → %q", b)
	}
	if resp, _ := do(t, "DELETE", base+"/things", ""); resp.StatusCode != 204 {
		t.Errorf("DELETE /things → %d", resp.StatusCode)
	}
}

func TestL6_3_NotFoundAndMethodNotAllowed(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) {
		r.Handle("GET /kv/{key}", say("get"))
		r.Handle("PUT /kv/{key}", say("put"))
		r.Handle("DELETE /kv/{key}", say("del"))
	})
	if resp, _ := do(t, "GET", base+"/nope", ""); resp.StatusCode != 404 {
		t.Errorf("an unknown path should be 404, got %d", resp.StatusCode)
	}
	resp, _ := do(t, "POST", base+"/kv/x", "body")
	if resp.StatusCode != 405 {
		t.Fatalf("a known path with the wrong method should be 405, got %d", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow != "DELETE, GET, HEAD, PUT" {
		t.Errorf("Allow = %q, want %q (sorted, and GET implies HEAD)", allow, "DELETE, GET, HEAD, PUT")
	}
}

func TestL6_3_Wildcards(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) {
		r.HandleFunc("GET /users/{id}/posts/{post}", func(w *httpraw.ResponseWriter, r *httpraw.Request) {
			w.Write([]byte(r.PathValue("id") + "|" + r.PathValue("post") + "|" + r.PathValue("missing")))
		})
		r.HandleFunc("GET /files/{path...}", func(w *httpraw.ResponseWriter, r *httpraw.Request) {
			w.Write([]byte(r.PathValue("path")))
		})
	})
	if _, b := do(t, "GET", base+"/users/42/posts/hello", ""); b != "42|hello|" {
		t.Errorf("two wildcards → %q, want %q (unknown names give \"\")", b, "42|hello|")
	}
	if _, b := do(t, "GET", base+"/files/a/b/c.txt", ""); b != "a/b/c.txt" {
		t.Errorf("{path...} takes the rest of the path, slashes included: got %q", b)
	}
	if _, b := do(t, "GET", base+"/files/", ""); b != "" {
		t.Errorf("{path...} may match nothing: got %q", b)
	}
	if resp, _ := do(t, "GET", base+"/users/42/posts", ""); resp.StatusCode != 404 {
		t.Errorf("too few segments should be 404, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", base+"/users//posts/x", ""); resp.StatusCode != 404 {
		t.Errorf("a {name} wildcard matches one non-empty segment; an empty one should be 404, got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "GET", base+"/users/42/posts/x/extra", ""); resp.StatusCode != 404 {
		t.Errorf("too many segments should be 404, got %d", resp.StatusCode)
	}
}

func TestL6_3_PercentDecoding(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) {
		r.HandleFunc("GET /kv/{key}", func(w *httpraw.ResponseWriter, r *httpraw.Request) {
			w.Write([]byte(r.PathValue("key")))
		})
	})
	if _, b := do(t, "GET", base+"/kv/a%2Fb%20c", ""); b != "a/b c" {
		t.Errorf("decode each segment after splitting on /: %%2F stays inside the key. Got %q, want %q", b, "a/b c")
	}
	c := rawDial(t, strings.TrimPrefix(base, "http://"))
	c.send("GET /kv/bad%zz HTTP/1.1\r\nConnection: close\r\n\r\n")
	if got := c.statusLine(); got != "HTTP/1.1 400 Bad Request" {
		t.Errorf("an invalid percent-escape should be 400, got %q", got)
	}
}

func TestL6_3_MostSpecificWins(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) {
		r.Handle("GET /kv/{key}", say("wildcard"))
		r.Handle("GET /kv/special", say("literal")) // registered second, still wins
		r.Handle("/health", say("any method"))
	})
	if _, b := do(t, "GET", base+"/kv/special", ""); b != "literal" {
		t.Errorf("a literal segment beats a wildcard regardless of registration order: got %q", b)
	}
	if _, b := do(t, "GET", base+"/kv/other", ""); b != "wildcard" {
		t.Errorf("got %q", b)
	}
	if _, b := do(t, "POST", base+"/health", "x"); b != "any method" {
		t.Errorf("a pattern with no method matches every method: got %q", b)
	}
}

func TestL6_3_HEADUsesGET(t *testing.T) {
	base := routed(t, func(r *httpraw.Router) { r.Handle("GET /x", say("hello")) })
	resp, b := do(t, "HEAD", base+"/x", "")
	if resp.StatusCode != 200 || b != "" || resp.ContentLength != 5 {
		t.Errorf("HEAD on a GET route: status %d, body %q, length %d; want 200, empty, 5", resp.StatusCode, b, resp.ContentLength)
	}
}

func TestL6_3_BadPatternsPanic(t *testing.T) {
	bad := []string{"", "GET", "GET  /x", "GET x", "x/y", "GET /a/{}", "GET /a/{x", "GET /a/{x}{y}", "GET /a/{x}/{x}", "GET /{rest...}/more"}
	for _, p := range bad {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Handle(%q) should panic: it's not a valid pattern", p)
				}
			}()
			httpraw.NewRouter().Handle(p, say(""))
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("registering the same pattern twice should panic")
			}
		}()
		r := httpraw.NewRouter()
		r.Handle("GET /dup/{a}", say(""))
		r.Handle("GET /dup/{b}", say(""))
	}()
}

func TestL6_3_KVRoutes(t *testing.T) {
	st := store.NewMemory()
	base := routed(t, func(r *httpraw.Router) { httpraw.KVRoutes(r, st) })
	url := base + "/v1/kv/greeting"

	if resp, _ := do(t, "GET", url, ""); resp.StatusCode != 404 {
		t.Errorf("GET a missing key → %d, want 404", resp.StatusCode)
	}
	if resp, _ := do(t, "PUT", url, "hello\x00world"); resp.StatusCode != 204 || resp.Header.Get("ETag") != `"1"` {
		t.Errorf("PUT → %d ETag %q, want 204 with ETag \"1\"", resp.StatusCode, resp.Header.Get("ETag"))
	}
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != 200 || body != "hello\x00world" {
		t.Errorf("GET → %d %q", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("values are bytes: Content-Type should be application/octet-stream, got %q", ct)
	}
	if et := resp.Header.Get("ETag"); et != `"1"` {
		t.Errorf("GET carries ETag: \"<version>\", got %q", et)
	}
	if resp, _ := do(t, "PUT", url, "again"); resp.Header.Get("ETag") != `"2"` {
		t.Errorf("PUT answers with the new version as its ETag: got %q, want %q", resp.Header.Get("ETag"), `"2"`)
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
	if resp, _ := do(t, "PUT", base+"/v1/kv/empty", ""); resp.StatusCode != 204 {
		t.Errorf("PUT with an empty body stores an empty value: %d", resp.StatusCode)
	}
	if resp, body := do(t, "GET", base+"/v1/kv/empty", ""); resp.StatusCode != 200 || body != "" {
		t.Errorf("GET of an empty value → %d %q, want 200 \"\"", resp.StatusCode, body)
	}
	big := bytes.Repeat([]byte("x"), 2<<20)
	if resp, _ := do(t, "PUT", base+"/v1/kv/big", string(big)); resp.StatusCode != 413 {
		t.Errorf("a 2 MiB PUT should hit the 1 MiB body limit (413), got %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", url, "x"); resp.StatusCode != 405 {
		t.Errorf("POST /v1/kv/{key} → %d, want 405", resp.StatusCode)
	}
}

func TestL6_3_KeysListing(t *testing.T) {
	st := store.NewMemory()
	for _, k := range []string{"user:2", "order:1", "user:1"} {
		st.Set(t.Context(), k, []byte("v"))
	}
	base := routed(t, func(r *httpraw.Router) { httpraw.KVRoutes(r, st) })
	resp, body := do(t, "GET", base+"/v1/keys", "")
	if body != "order:1\nuser:1\nuser:2\n" {
		t.Errorf("GET /v1/keys = %q, want one key per line, sorted, with a trailing newline", body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if _, body := do(t, "GET", base+"/v1/keys?prefix=user%3A", ""); body != "user:1\nuser:2\n" {
		t.Errorf("GET /v1/keys?prefix=user: = %q", body)
	}
	if _, body := do(t, "GET", base+"/v1/keys?prefix=zzz", ""); body != "" {
		t.Errorf("no matches is an empty body, got %q", body)
	}
}

func TestL6_3_EncodedSlashInKey(t *testing.T) {
	st := store.NewMemory()
	base := routed(t, func(r *httpraw.Router) { httpraw.KVRoutes(r, st) })
	do(t, "PUT", base+"/v1/kv/a%2Fb", "slashy")
	if e, err := st.Get(t.Context(), "a/b"); err != nil || string(e.Value) != "slashy" {
		t.Errorf("/v1/kv/a%%2Fb should store key \"a/b\": (%q, %v)", e.Value, err)
	}
}
