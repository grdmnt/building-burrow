# 7.2 · JSON In and Out

Decode untrusted JSON strictly into typed requests, encode typed responses, and give every error one shape. From here on every endpoint, including the ones from 7.1, returns `ErrorResponse`.

## Build

```go
// api/json.go
type KV struct {
	Key       string     `json:"key"`
	Value     []byte     `json:"value"`              // base64 on the wire, automatic
	Version   uint64     `json:"version"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type Duration time.Duration // JSON string like "10s"; implements json.Marshaler and json.Unmarshaler

type PutRequest struct {
	Value     []byte   `json:"value"`
	IfVersion *uint64  `json:"if_version,omitempty"` // nil: unconditional; 0: create only
	TTL       Duration `json:"ttl,omitempty"`
}

type ErrorResponse struct {
	Error string `json:"error"` // human text
	Code  string `json:"code"`  // not_found | version_mismatch | invalid_key | invalid_request | too_large | method_not_allowed | internal
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
//   GET  /v1/kv/{key} with Accept: application/json      -> 200 KV (otherwise octet-stream as in 7.1)
//   PUT  /v1/kv/{key} with Content-Type: application/json -> body is PutRequest; 200 KV
//   GET  /v1/keys with Accept: application/json           -> 200 ["k1","k2"]
//   POST /v1/batch  {"ops": [...]}                         -> 200 {"results": [BatchResult...]} in order; ops run independently (not a transaction)
// Every error from every endpoint (including 7.1's) is now application/json ErrorResponse.
// Decoding: json.NewDecoder with DisallowUnknownFields, body limited to 1 MiB, exactly one JSON value
// (trailing data is invalid_request), unknown fields name the field in Error, bad base64 is invalid_request.
// Batch size limit 1000 ops -> too_large.
// Conditional writes (if_version present) that fail their precondition are version_mismatch (412 on
// /v1/kv, the same code in a BatchResult), including when the key is missing; not_found is only for
// unconditional operations. ttl must be a JSON string like "10s"; a bare number is invalid_request.
```

- `KV`, `PutRequest`, `ErrorResponse`, `BatchOp`, `BatchResult` with struct tags
- `Duration` with MarshalJSON/UnmarshalJSON
- One `decodeJSON(r, &v)` helper (DisallowUnknownFields, size limit, single value) and one `writeJSON(w, status, v)`
- Content negotiation on GET/PUT, `POST /v1/batch`, JSON errors everywhere

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
curl -s -H 'Accept: application/json' localhost:8080/v1/kv/greeting | jq
curl -s -X POST localhost:8080/v1/batch -d '{"ops":[{"op":"put","key":"a","value":"aGk="},{"op":"get","key":"a"}]}' | jq
```

## Uses

- HTTP API (built in 7.1 · Front Panel)

## In the wild

encoding/json (json/v2 is coming as an experiment in later Go releases); go-playground/validator for richer validation; this error shape is RFC 9457 problem details without the ceremony

## New in Go

struct tags · json.Decoder vs json.Unmarshal · DisallowUnknownFields · json.Marshaler/Unmarshaler · pointer fields for optional values · []byte as base64 · content negotiation

Standard library: `encoding/json`, `net/http`, `io`, `mime`, `strings`
