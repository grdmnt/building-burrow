# 7.2 · JSON In and Out

> Bytes in a body were fine for one value. Real clients want structure, and they want every error to look the same.

## Your goal

Teach the HTTP API to speak JSON. A client that sends `Accept: application/json` gets a `GET` response as a JSON object with the key, the value and the version; a client that sends a `PUT` with `Content-Type: application/json` sends a `PutRequest` object instead of raw bytes. A new `POST /v1/batch` runs up to 1000 operations in one request. And from now on every error from every endpoint, including the ones from 7.1, is a JSON `ErrorResponse` with a machine-readable `code` and a human-readable `error`.

The hard part is decoding *untrusted* input strictly: unknown fields, trailing data, the wrong type, bad base64 and oversized bodies all have to be rejected with a message that says what was wrong.

## What you're using from before

**HTTP API (7.1 · Front Panel).** The five routes, the `If-Match` and `?ttl=` handling and the error mapping stay. This level adds a JSON branch to `GET` and `PUT`, one new route, and replaces the plain-text error helper with a JSON one. The `PutRequest` fields map onto the same four store calls as 7.1's headers did:

```go
// 7.1 request                     // 7.2 PutRequest
If-Match: "3"                      {"if_version": 3, ...}
?ttl=10s                           {"ttl": "10s", ...}
raw body                           {"value": "<base64>"}
```

So `if_version` absent means `Set`, `if_version` present means `CompareAndSwap`, `ttl` present picks the `WithTTL` variant, and the 412-even-when-missing rule is unchanged, now spelled `version_mismatch`.

## What's new here

**Struct tags.** A struct field can carry a string literal after its type, and `encoding/json` reads the `json:"..."` part to learn the field's name on the wire and a few options. Only exported fields (capitalised) are encoded at all:

```go
type KV struct {
    Key       string     `json:"key"`
    Value     []byte     `json:"value"`
    Version   uint64     `json:"version"`
    ExpiresAt *time.Time `json:"expires_at,omitempty"`
}
b, _ := json.Marshal(KV{Key: "k", Value: []byte("hi"), Version: 1})
// {"key":"k","value":"aGk=","version":1}
```

**`[]byte` is base64 on the wire.** A `[]byte` field is encoded as a base64 string and decoded back automatically, so binary values survive JSON. Two traps: a *nil* slice encodes as `null`, not `""`, so make sure an empty value is `[]byte{}` before encoding; and bad base64 on the way in is a decode error that you should report as `invalid_request`.

**Pointer fields for optional values.** JSON cannot tell `"if_version": 0` from an absent `if_version` if the field is a plain `uint64`, because both decode to 0. A `*uint64` field is nil when absent and points at 0 when the client sent 0, and 0 matters here (it means create-only). The same reasoning makes `ExpiresAt` a `*time.Time`.

**`omitempty` versus `omitzero`.** `omitempty` leaves out a nil pointer, an empty string, slice or map, and a zero number, but never a zero struct, which is why `ExpiresAt` is a pointer. Go 1.24 adds `omitzero`: `json:"expires_at,omitzero"` omits a `time.Time` whose `IsZero()` reports true (any type's `IsZero() bool` method is honoured; otherwise the zero value is compared). Keep the pointer here because the tests read it, but reach for `omitzero` in your own code. For the `Duration` fields either tag works, since the underlying type is a number.

**`json.Marshaler` and `json.Unmarshaler`.** A type with a `MarshalJSON() ([]byte, error)` method decides its own encoding, and one with `UnmarshalJSON([]byte) error` its own decoding. `Duration` uses them to appear as `"1m30s"` instead of a count of nanoseconds. `UnmarshalJSON` receives the raw JSON of that one value, so a bare number arrives as `10` rather than `"10"` and is rejected:

```go
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
    return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
    var s string
    if err := json.Unmarshal(b, &s); err != nil {
        return fmt.Errorf("ttl must be a string like \"10s\"")
    }
    // time.ParseDuration(s); reject negative; *d = Duration(v)
}
```

**`json.Decoder` versus `json.Unmarshal`.** `json.Unmarshal(b, &v)` works on a `[]byte` you already hold. `json.NewDecoder(r)` reads from an `io.Reader` and has two things you need for untrusted input: `DisallowUnknownFields()`, so `{"valeu": ...}` is an error whose text names the field instead of being silently ignored, and `More()`, which reports whether anything is left after the first value, so `{"a":1}{"a":2}` and `{"a":1} x` are both caught. Combine it with `http.MaxBytesReader` so a 2 MiB body is cut off at 1 MiB:

```go
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
    dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
    dec.DisallowUnknownFields()
    if err := dec.Decode(v); err != nil {
        return err // *http.MaxBytesError -> 413; everything else -> 400
    }
    if dec.More() {
        return errors.New("unexpected data after the JSON value")
    }
    return nil
}
```

A `*json.SyntaxError` and `*json.UnmarshalTypeError` carry offsets and field names; `errors.As` them to build a message that points at the problem. An empty body decodes as `io.EOF`, which is also `invalid_request`.

**Content negotiation.** The request says what it wants with `Accept` and what it is sending with `Content-Type`. The rule here is simple: `Accept` containing `application/json` means answer in JSON; a `PUT` whose `Content-Type` is `application/json` carries a `PutRequest`, anything else is a raw value. `mime.ParseMediaType("application/json; charset=utf-8")` returns `"application/json"` and drops the parameters.

## What to do

1. Create `api/json.go` with `KV`, `Duration`, `PutRequest`, `ErrorResponse`, `BatchOp` and `BatchResult` exactly as in the Reference, plus `MarshalJSON` and `UnmarshalJSON` on `Duration`.
2. Write `writeJSON(w, status, v)`: set `Content-Type: application/json`, write the status, encode `v`. Write `writeError(w, status, code, msg)` on top of it and switch every error path from 7.1 to it. Map: `ErrNotFound` to `not_found`, `ErrVersionMismatch` to `version_mismatch`, `ErrInvalidKey` to `invalid_key`, bad `If-Match`, bad `?ttl=` and every decode failure to `invalid_request`, `*http.MaxBytesError` to `too_large`.
3. Write `decodeJSON` as above.
4. `GET /v1/kv/{key}`: when `Accept` contains `application/json`, answer `200` with a `KV` (`expires_at` in UTC, only when set; `Value` never nil). Otherwise the 7.1 octet-stream response, unchanged.
5. `PUT /v1/kv/{key}`: when `Content-Type` is `application/json`, decode a `PutRequest`, map `if_version` and `ttl` to the four store calls, and answer `200` with the `KV`. Otherwise the raw 7.1 path with its `204`.
6. `GET /v1/keys` with JSON: a JSON array, `[]` when empty (a nil slice would encode as `null`).
7. `POST /v1/batch`: decode `{"ops": [...]}`; more than 1000 ops is `413 too_large`. Run each op in order against the store and collect a `BatchResult` per op: `get`, `put` (same mapping as `PUT`), `delete` (`CompareAndDelete` when `if_version` is present). An unknown op or a failing op produces a result with `ok: false` and an `error`, and the batch continues. Answer `200 {"results": [...]}`, with `[]` for no ops.
8. `405`: the mux writes its own plain-text 405, so register a method-less fallback pattern for each path (`/v1/kv/{key}`, `/v1/kv/{key}/touch`, `/v1/keys`, `/v1/batch`) whose handler sets `Allow` and writes a JSON `method_not_allowed`. The method-specific patterns are more specific, so they still win when the method matches.

## Example

```bash
$ curl -s -X PUT -H 'Content-Type: application/json' \
    -d '{"value":"aGVsbG8=","ttl":"1h"}' localhost:8080/v1/kv/greeting
{"key":"greeting","value":"aGVsbG8=","version":1,
 "expires_at":"2026-10-31T10:30:41.123456789Z"}
$ curl -s -H 'Accept: application/json' localhost:8080/v1/kv/greeting
{"key":"greeting","value":"aGVsbG8=","version":1,
 "expires_at":"2026-10-31T10:30:41.123456789Z"}
$ curl -s -X PUT -H 'Content-Type: application/json' \
    -d '{"valeu":"aGk="}' localhost:8080/v1/kv/greeting
{"error":"json: unknown field \"valeu\"","code":"invalid_request"}
$ curl -s -X PUT -H 'Content-Type: application/json' \
    -d '{"value":"aGk=","if_version":7}' localhost:8080/v1/kv/greeting
{"error":"version mismatch","code":"version_mismatch"}
$ curl -s localhost:8080/v1/kv/missing
{"error":"not found","code":"not_found"}
$ curl -s -X POST localhost:8080/v1/batch -d '{"ops":[
    {"op":"put","key":"a","value":"aGk="},
    {"op":"get","key":"a"},
    {"op":"get","key":"nope"},
    {"op":"frobnicate","key":"a"}]}'
{"results":[
 {"ok":true,"entry":{"key":"a","value":"aGk=","version":1}},
 {"ok":true,"entry":{"key":"a","value":"aGk=","version":1}},
 {"ok":false,"error":{"error":"not found","code":"not_found"}},
 {"ok":false,"error":{"error":"unknown op \"frobnicate\"",
                      "code":"invalid_request"}}]}
```

(Real output is on one line; it is wrapped here to fit.)

## Done when

- [ ] `GET` with `Accept: application/json` returns `key`, base64 `value` and `version`; `expires_at` is omitted without a TTL and is RFC 3339 UTC with one; an empty value is `"value":""`; without the header the 7.1 response is unchanged
- [ ] `PUT` with a JSON body returns `200` and the `KV`; `if_version` 0, match and mismatch behave like `If-Match`; `ttl "50ms"` expires the key; `ttl` of `"abc"`, `"-1s"` or the number `10` is `400 invalid_request`; `Content-Type: text/plain` still means raw (`204`)
- [ ] An unknown field (named in the error), two values, trailing garbage, an empty body, bad base64, a wrong type and an array instead of an object are all `400 invalid_request`; a 2 MiB JSON body is `413 too_large`
- [ ] Every error is `application/json` with the right code: `not_found`, `version_mismatch`, `invalid_key`, `method_not_allowed` (with `Allow` kept), `too_large`, `invalid_request`
- [ ] `GET /v1/keys` with JSON is `[]` for no keys and a sorted array otherwise
- [ ] A batch of 1000 mixed ops returns 1000 results in order; an unknown op and a missing key are per-op errors, not a failed batch; an empty `ops` array is fine; 1001 ops is `413 too_large`
- [ ] Conditional batch ops follow the `If-Match` rules, including `version_mismatch` for a conditional op on a missing key and an expiry for `put` with `if_version` and `ttl`
- [ ] `Duration(90s)` marshals to `"1m30s"` and back, `"forever"` fails, a zero `ttl` is omitted from `PutRequest`, and a `KV` with an empty value and no expiry encodes without `expires_at`

## Reference

The exact contract the tests hold you to.

```go
// api/json.go
type KV struct {
	Key       string     `json:"key"`
	Value     []byte     `json:"value"` // base64 on the wire, automatic
	Version   uint64     `json:"version"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Duration is a JSON string like "10s". It implements
// json.Marshaler and json.Unmarshaler.
type Duration time.Duration

type PutRequest struct {
	Value     []byte   `json:"value"`
	// nil: unconditional; 0: create only
	IfVersion *uint64  `json:"if_version,omitempty"`
	TTL       Duration `json:"ttl,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"` // human text
	// not_found | version_mismatch | invalid_key | invalid_request
	// | too_large | method_not_allowed | internal
	Code string `json:"code"`
}

type BatchOp struct {
	Op        string   `json:"op"` // get | put | delete
	Key       string   `json:"key"`
	Value     []byte   `json:"value,omitempty"`
	IfVersion *uint64  `json:"if_version,omitempty"`
	TTL       Duration `json:"ttl,omitempty"`
}
type BatchResult struct {
	OK    bool           `json:"ok"`
	Entry *KV            `json:"entry,omitempty"`
	Error *ErrorResponse `json:"error,omitempty"`
}

// Endpoints:
//   GET  /v1/kv/{key} with Accept: application/json
//     -> 200 KV (otherwise octet-stream as in 7.1)
//   PUT  /v1/kv/{key} with Content-Type: application/json
//     -> body is PutRequest; 200 KV
//   GET  /v1/keys with Accept: application/json
//     -> 200 ["k1","k2"]
//   POST /v1/batch  {"ops": [...]}
//     -> 200 {"results": [BatchResult...]} in order; ops run
//        independently (not a transaction)
// Every error from every endpoint (including 7.1's) is now
// application/json ErrorResponse.
// Decoding: json.NewDecoder with DisallowUnknownFields, body limited
// to 1 MiB, exactly one JSON value (trailing data is invalid_request),
// unknown fields name the field in Error, bad base64 is
// invalid_request.
// Batch size limit 1000 ops -> too_large.
// Conditional writes (if_version present) that fail their
// precondition are version_mismatch (412 on /v1/kv, the same code in a
// BatchResult), including when the key is missing; not_found is only
// for unconditional operations. ttl must be a JSON string like "10s";
// a bare number is invalid_request.
```

Decisions the tests rely on:

- A request wants JSON when its `Accept` header contains `application/json` (substring; q-values are ignored). A `PUT` carries `PutRequest` when its `Content-Type` is `application/json`; any other content type means the body is the raw value.
- `PUT` with a JSON body returns 200 and the `KV`; the raw path still returns 204.
- `GET /v1/keys` with JSON returns `[]` for no keys, never `null`. `expires_at` is RFC 3339 with nanoseconds, in UTC.
- Batch ops: `get`, `put` (with the same `if_version` and `ttl` mapping as `PUT`), `delete` (with `if_version` meaning `CompareAndDelete`). More than 1000 ops is 413 `too_large`.
- A conditional `put` or `delete` whose precondition fails is `version_mismatch` (412 on `/v1/kv`, the same code inside a `BatchResult`), including when the key is missing; `not_found` is only for unconditional operations.
- `ttl` must be a JSON string such as `"10s"`; a bare number is 400 `invalid_request`.
- `Duration` marshals as `time.Duration.String()`; a negative duration is `invalid_request`; zero is omitted.

## The tests check

- GET with Accept: application/json returns key, base64 value and version; expires_at is omitted without a TTL and is RFC 3339 UTC with one; an empty value encodes as `"value": ""`; without the Accept header the 7.1 octet-stream response is unchanged
- PUT JSON: value only is 200 with the KV; if_version mismatch is 412 version_mismatch; a match returns version 2; if_version 0 is 412 when the key exists and creates when it doesn't; ttl "50ms" sets expires_at and the key is gone after 100 ms; ttl "abc", "-1s" and the number 10 are 400 invalid_request
- Content-Type: text/plain on PUT still means the body is the raw value (204)
- Strict decoding, all 400 invalid_request: an unknown field (the error names `valeu`), two JSON values, trailing garbage, an empty body, bad base64, a string where a number is expected, an array instead of an object; a 2 MiB JSON body is 413 too_large
- Every error is application/json ErrorResponse: 404 not_found, 412 version_mismatch, 400 invalid_key, 405 method_not_allowed (Allow kept), 413 too_large, 400 invalid_request for a bad ?ttl= and for If-Match `*`
- GET /v1/keys with JSON returns `[]` for no keys, never null, and `["a","b"]` sorted
- Batch: 1000 mixed ops return 1000 results in order; a put result carries its entry; an unknown op gets an invalid_request result without failing the batch; a get of a missing key gets a not_found result; a failing op doesn't stop the rest; an empty ops array is fine; 1001 ops is 413 too_large
- Conditional batch ops: put with if_version 0 succeeds then fails, put with if_version and ttl sets an expiry (CompareAndSwapWithTTL), delete with a stale if_version fails and with the right one succeeds; put and delete with if_version on a missing key are version_mismatch results
- Duration(90s) marshals to "1m30s" and back; "forever" fails to unmarshal; a zero ttl is omitted from PutRequest; a KV with an empty value and no expiry encodes without expires_at

## Try it

```bash
./burrow watch
curl -s -H 'Accept: application/json' localhost:8080/v1/kv/greeting | jq
curl -s -X POST localhost:8080/v1/batch -d '{"ops":[
  {"op":"put","key":"a","value":"aGk="},{"op":"get","key":"a"}]}' | jq
```

## In the wild

encoding/json (json/v2 is coming as an experiment in later Go releases); go-playground/validator for richer validation; this error shape is RFC 9457 problem details without the ceremony.

## New in Go

struct tags · []byte as base64 · pointer fields for optional values · omitempty vs omitzero (Go 1.24) · json.Marshaler/Unmarshaler · json.Decoder vs json.Unmarshal · DisallowUnknownFields · content negotiation

Standard library: `encoding/json`, `net/http`, `io`, `mime`, `strings`
