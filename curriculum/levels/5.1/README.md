# 5.1 · Front Panel

Rebuild the Chapter 4 API on net/http and compare how much code disappears.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/5.1/files/`, matching the earlier chapters.

## Build

- `http.ServeMux` routes: `GET`, `PUT` and `DELETE /v1/kv/{key}`
- Read the key with `r.PathValue("key")`
- JSON errors shaped `{"error": "..."}`
- Keep your httpraw server around so you can compare the two

## Pass when

The same curl commands from 4.3 work. A wrong method gets 405 with an `Allow` header, and a 2 MB body gets 413.

## New in Go

handlers · method and path patterns · MaxBytesReader · JSON encoding

Standard library: `net/http` · `encoding/json` · `io`
