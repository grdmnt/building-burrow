# 2.2 · Line Protocol

A text protocol so you can drive the store by hand from `nc`.

## Build

```go
// wire/lines.go
// ServeLines serves the line protocol backed by st. Same return rules as ServeEcho.
func ServeLines(ln net.Listener, st store.Store, logger *slog.Logger) error
```

One command per line. Lines end in `\n`; a trailing `\r` is stripped. Replies end in `\n`. Commands are case-insensitive, keys are not.

| Request | Reply |
|---|---|
| `SET <key> <value…>` | `OK` · the value is everything after the second space, spaces included. `SET k ` (trailing space) sets an empty value |
| `GET <key>` | `VALUE <value>` or `NOT_FOUND` |
| `DEL <key>` | `OK` or `NOT_FOUND` |
| `KEYS [prefix]` | `KEYS <n>`, then one key per line, sorted |
| `PING [message]` | `PONG` or `PONG <message>` |
| `QUIT` | `BYE`, then the server closes the connection |

Errors start with `ERR `:

- unknown command → `ERR unknown command "FROB"` (quoted as sent)
- wrong argument count → `ERR wrong number of arguments for GET`
- invalid key → `ERR invalid key…` (the store's error text)
- a line longer than 64 KiB → `ERR line too long`, then close

Errors don't close the connection (except the long line). Blank lines are ignored.

## The tests check

Every row above, plus: pipelined commands in one write get replies in order, a command split across several TCP writes still parses, CRLF line endings, and two clients share one store.

## Try it

```bash
nc localhost 7070
SET name gerard
GET name
```

## New in Go

`bufio.Scanner` and its buffer limit · `bufio.ErrTooLong` · `strings.Cut` · `bufio.Writer` + `Flush`
