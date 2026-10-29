package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"burrow/api"
)

func jsonDo(t *testing.T, method, url, body string, headers ...string) (int, string, map[string]any) {
	t.Helper()
	resp, b := do(t, method, url, body, append([]string{"Accept", "application/json"}, headers...)...)
	var m map[string]any
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal([]byte(b), &m); err != nil && !strings.HasPrefix(strings.TrimSpace(b), "[") {
			t.Fatalf("%s %s: response isn't valid JSON: %q", method, url, b)
		}
	}
	return resp.StatusCode, b, m
}

func errCode(t *testing.T, m map[string]any) string {
	t.Helper()
	if m == nil {
		return "(not JSON)"
	}
	c, _ := m["code"].(string)
	if _, ok := m["error"].(string); !ok {
		t.Errorf("an error response has a string \"error\" field: %v", m)
	}
	return c
}

func TestL7_2_GetJSON(t *testing.T) {
	base, st := newAPI(t)
	st.Set(t.Context(), "k", []byte("hi\x00"))
	st.SetWithTTL(t.Context(), "t", []byte(""), time.Hour)
	code, _, m := jsonDo(t, "GET", base+"/v1/kv/k", "")
	if code != 200 || m["key"] != "k" || m["value"] != base64.StdEncoding.EncodeToString([]byte("hi\x00")) || m["version"] != float64(1) {
		t.Errorf("GET with Accept: application/json → %d %v, want key, base64 value and version", code, m)
	}
	if _, ok := m["expires_at"]; ok {
		t.Errorf("expires_at is omitted when there's no TTL: %v", m)
	}
	_, raw, m := jsonDo(t, "GET", base+"/v1/kv/t", "")
	if m["value"] != "" {
		t.Errorf("an empty value encodes as \"value\": \"\", got %s", raw)
	}
	exp, _ := m["expires_at"].(string)
	ts, err := time.Parse(time.RFC3339Nano, exp)
	if err != nil || ts.Location() != time.UTC || time.Until(ts) < 59*time.Minute {
		t.Errorf("expires_at should be RFC 3339 in UTC, about an hour away: %q (%v)", exp, err)
	}
	resp, body := do(t, "GET", base+"/v1/kv/k", "")
	if resp.Header.Get("Content-Type") != "application/octet-stream" || body != "hi\x00" {
		t.Errorf("without Accept: application/json the raw response from 7.1 is unchanged")
	}
}

func TestL7_2_PutJSON(t *testing.T) {
	base, _ := newAPI(t)
	ct := []string{"Content-Type", "application/json"}
	val := base64.StdEncoding.EncodeToString([]byte("v1"))
	code, raw, m := jsonDo(t, "PUT", base+"/v1/kv/k", `{"value":"`+val+`"}`, ct...)
	if code != 200 || m["version"] != float64(1) {
		t.Fatalf("PUT JSON → %d %s, want 200 and the KV", code, raw)
	}
	if code, _, m := jsonDo(t, "PUT", base+"/v1/kv/k", `{"value":"`+val+`","if_version":7}`, ct...); code != 412 || errCode(t, m) != "version_mismatch" {
		t.Errorf("if_version mismatch → %d %v, want 412 version_mismatch", code, m)
	}
	if code, _, m := jsonDo(t, "PUT", base+"/v1/kv/k", `{"value":"`+val+`","if_version":1}`, ct...); code != 200 || m["version"] != float64(2) {
		t.Errorf("if_version match → %d %v, want 200 version 2", code, m)
	}
	if code, _, _ := jsonDo(t, "PUT", base+"/v1/kv/k", `{"value":"`+val+`","if_version":0}`, ct...); code != 412 {
		t.Errorf("if_version 0 means create only → %d, want 412", code)
	}
	if code, _, m := jsonDo(t, "PUT", base+"/v1/kv/new", `{"value":"`+val+`","if_version":0}`, ct...); code != 200 || m["version"] != float64(1) {
		t.Errorf("if_version 0 on a missing key creates it → %d %v", code, m)
	}
	code, _, m = jsonDo(t, "PUT", base+"/v1/kv/temp", `{"value":"`+val+`","ttl":"50ms"}`, ct...)
	if code != 200 || m["expires_at"] == nil {
		t.Errorf("ttl \"50ms\" → %d %v, want expires_at set", code, m)
	}
	time.Sleep(100 * time.Millisecond)
	if code, _, _ := jsonDo(t, "GET", base+"/v1/kv/temp", ""); code != 404 {
		t.Errorf("the key should be gone after its ttl: %d", code)
	}
	for _, ttl := range []string{`"abc"`, `"-1s"`, `10`} {
		if code, _, m := jsonDo(t, "PUT", base+"/v1/kv/k", `{"value":"`+val+`","ttl":`+ttl+`}`, ct...); code != 400 || errCode(t, m) != "invalid_request" {
			t.Errorf("ttl %s → %d %v, want 400 invalid_request", ttl, code, m)
		}
	}
	resp, _ := do(t, "PUT", base+"/v1/kv/raw", `{"value":"not json mode"}`, "Content-Type", "text/plain")
	if resp.StatusCode != 204 {
		t.Errorf("Content-Type: text/plain on PUT still means the body is the raw value (204), got %d", resp.StatusCode)
	}
	if _, body := do(t, "GET", base+"/v1/kv/raw", ""); body != `{"value":"not json mode"}` {
		t.Errorf("raw PUT stored %q", body)
	}
}

func TestL7_2_StrictDecoding(t *testing.T) {
	base, _ := newAPI(t)
	ct := []string{"Content-Type", "application/json"}
	cases := []struct {
		name, body, mention string
		status              int
	}{
		{"unknown field", `{"valeu":"aGk="}`, "valeu", 400},
		{"two values", `{"value":"aGk="}{"value":"aGk="}`, "", 400},
		{"trailing garbage", `{"value":"aGk="} x`, "", 400},
		{"empty body", ``, "", 400},
		{"bad base64", `{"value":"%%%"}`, "", 400},
		{"wrong type", `{"value":"aGk=","if_version":"one"}`, "", 400},
		{"not an object", `["aGk="]`, "", 400},
	}
	for _, c := range cases {
		code, raw, m := jsonDo(t, "PUT", base+"/v1/kv/k", c.body, ct...)
		if code != c.status || errCode(t, m) != "invalid_request" {
			t.Errorf("%s: → %d %s, want 400 invalid_request", c.name, code, raw)
		}
		if c.mention != "" && !strings.Contains(raw, c.mention) {
			t.Errorf("%s: the error should name %q: %s", c.name, c.mention, raw)
		}
	}
	big := `{"value":"` + strings.Repeat("A", 2<<20) + `"}`
	if code, _, m := jsonDo(t, "PUT", base+"/v1/kv/k", big, ct...); code != 413 || errCode(t, m) != "too_large" {
		t.Errorf("a 2 MiB JSON body → %d %v, want 413 too_large", code, m)
	}
}

func TestL7_2_ErrorsAreJSON(t *testing.T) {
	base, _ := newAPI(t)
	do(t, "PUT", base+"/v1/kv/k", "v")
	cases := []struct {
		name, method, url, body string
		headers                 []string
		status                  int
		code                    string
	}{
		{"missing key", "GET", "/v1/kv/missing", "", nil, 404, "not_found"},
		{"stale If-Match", "PUT", "/v1/kv/k", "x", []string{"If-Match", `"9"`}, 412, "version_mismatch"},
		{"invalid key", "PUT", "/v1/kv/" + strings.Repeat("k", 300), "x", nil, 400, "invalid_key"},
		{"wrong method", "PATCH", "/v1/kv/k", "x", nil, 405, "method_not_allowed"},
		{"too large", "PUT", "/v1/kv/k", strings.Repeat("x", 2<<20), nil, 413, "too_large"},
		{"bad ttl", "PUT", "/v1/kv/k?ttl=soon", "x", nil, 400, "invalid_request"},
		{"bad If-Match", "PUT", "/v1/kv/k", "x", []string{"If-Match", "*"}, 400, "invalid_request"},
	}
	for _, c := range cases {
		resp, body := do(t, c.method, base+c.url, c.body, c.headers...)
		var m map[string]any
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || json.Unmarshal([]byte(body), &m) != nil {
			t.Errorf("%s: every error is application/json ErrorResponse, got %q %q", c.name, resp.Header.Get("Content-Type"), body)
			continue
		}
		if resp.StatusCode != c.status || m["code"] != c.code {
			t.Errorf("%s → %d %v, want %d %s", c.name, resp.StatusCode, m, c.status, c.code)
		}
		if c.status == 405 && resp.Header.Get("Allow") == "" {
			t.Errorf("a JSON 405 must keep the Allow header")
		}
	}
}

func TestL7_2_KeysJSON(t *testing.T) {
	base, st := newAPI(t)
	_, raw, _ := jsonDo(t, "GET", base+"/v1/keys", "")
	if strings.TrimSpace(raw) != "[]" {
		t.Errorf("no keys in JSON is [], never null: got %q", raw)
	}
	st.Set(t.Context(), "b", []byte("v"))
	st.Set(t.Context(), "a", []byte("v"))
	_, raw, _ = jsonDo(t, "GET", base+"/v1/keys", "")
	var keys []string
	if json.Unmarshal([]byte(raw), &keys) != nil || len(keys) != 2 || keys[0] != "a" {
		t.Errorf("GET /v1/keys with JSON = %s, want [\"a\",\"b\"]", raw)
	}
}

func TestL7_2_Batch(t *testing.T) {
	base, _ := newAPI(t)
	var ops []map[string]any
	for i := range 1000 {
		k := fmt.Sprintf("k%d", i%50)
		switch i % 4 {
		case 0, 1:
			ops = append(ops, map[string]any{"op": "put", "key": k, "value": base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(i)))})
		case 2:
			ops = append(ops, map[string]any{"op": "get", "key": k})
		case 3:
			ops = append(ops, map[string]any{"op": "delete", "key": k})
		}
	}
	ops[5] = map[string]any{"op": "frobnicate", "key": "k"}
	ops[6] = map[string]any{"op": "get", "key": "never-set"}
	body, _ := json.Marshal(map[string]any{"ops": ops})
	resp, raw := do(t, "POST", base+"/v1/batch", string(body), "Content-Type", "application/json")
	if resp.StatusCode != 200 {
		t.Fatalf("batch → %d %s", resp.StatusCode, raw)
	}
	var out struct {
		Results []api.BatchResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out.Results) != 1000 {
		t.Fatalf("batch of 1000 ops should return 1000 results in order: %d (%v)", len(out.Results), err)
	}
	if r := out.Results[0]; !r.OK || r.Entry == nil || string(r.Entry.Value) != "0" {
		t.Errorf("result 0 (put) = %+v", r)
	}
	if r := out.Results[5]; r.OK || r.Error == nil || r.Error.Code != "invalid_request" {
		t.Errorf("an unknown op gets an invalid_request result, not a failed batch: %+v", r)
	}
	if r := out.Results[6]; r.OK || r.Error == nil || r.Error.Code != "not_found" {
		t.Errorf("a get of a missing key → not_found result: %+v", r)
	}
	if !out.Results[999].OK && out.Results[999].Error == nil {
		t.Errorf("a failing op doesn't stop the rest")
	}
	resp, raw = do(t, "POST", base+"/v1/batch", `{"ops":[]}`, "Content-Type", "application/json")
	if resp.StatusCode != 200 || !strings.Contains(raw, `"results":[]`) {
		t.Errorf("an empty ops array is fine: %d %s", resp.StatusCode, raw)
	}
	big, _ := json.Marshal(map[string]any{"ops": make([]map[string]any, 1001)})
	big = bytes.ReplaceAll(big, []byte("null"), []byte(`{"op":"get","key":"k"}`))
	if resp, raw := do(t, "POST", base+"/v1/batch", string(big), "Content-Type", "application/json"); resp.StatusCode != 413 || !strings.Contains(raw, "too_large") {
		t.Errorf("1001 ops → %d %s, want 413 too_large", resp.StatusCode, raw)
	}
}

func TestL7_2_BatchConditional(t *testing.T) {
	base, _ := newAPI(t)
	body := `{"ops":[
		{"op":"put","key":"c","value":"YQ==","if_version":0},
		{"op":"put","key":"c","value":"Yg==","if_version":0},
		{"op":"put","key":"c","value":"Yw==","if_version":1,"ttl":"1h"},
		{"op":"delete","key":"c","if_version":1},
		{"op":"delete","key":"c","if_version":2},
		{"op":"put","key":"ghost","value":"YQ==","if_version":5},
		{"op":"delete","key":"ghost","if_version":1}
	]}`
	_, raw := do(t, "POST", base+"/v1/batch", body, "Content-Type", "application/json")
	var out struct {
		Results []api.BatchResult `json:"results"`
	}
	json.Unmarshal([]byte(raw), &out)
	want := []bool{true, false, true, false, true, false, false}
	for i, ok := range want {
		if i >= len(out.Results) || out.Results[i].OK != ok {
			t.Errorf("conditional batch op %d: ok should be %v; results %s", i, ok, raw)
		}
	}
	for _, i := range []int{5, 6} {
		if i < len(out.Results) && (out.Results[i].Error == nil || out.Results[i].Error.Code != "version_mismatch") {
			t.Errorf("op %d: a conditional op on a missing key fails its precondition: want version_mismatch, got %+v", i, out.Results[i].Error)
		}
	}
	if len(out.Results) > 2 && out.Results[2].Entry != nil && out.Results[2].Entry.ExpiresAt == nil {
		t.Errorf("a put with if_version and ttl sets an expiry (CompareAndSwapWithTTL)")
	}
}

func TestL7_2_Duration(t *testing.T) {
	b, err := json.Marshal(api.Duration(90 * time.Second))
	if err != nil || string(b) != `"1m30s"` {
		t.Errorf("Duration(90s) marshals to %s (%v), want \"1m30s\"", b, err)
	}
	var d api.Duration
	if err := json.Unmarshal([]byte(`"1m30s"`), &d); err != nil || d != api.Duration(90*time.Second) {
		t.Errorf("unmarshal \"1m30s\" → %v (%v)", time.Duration(d), err)
	}
	if err := json.Unmarshal([]byte(`"forever"`), &d); err == nil {
		t.Errorf("unmarshal \"forever\" should fail")
	}
	b, _ = json.Marshal(api.PutRequest{Value: []byte("x")})
	if strings.Contains(string(b), "ttl") {
		t.Errorf("a zero ttl is omitted: %s", b)
	}
	b, err = json.Marshal(api.KV{Key: "k", Value: []byte{}})
	if err != nil || !strings.Contains(string(b), `"value":""`) || strings.Contains(string(b), "expires_at") {
		t.Errorf("KV with an empty value and no expiry encodes as %s (%v)", b, err)
	}
}
