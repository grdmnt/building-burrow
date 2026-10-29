# S4.2 · Filters

Subscriptions choose which messages they want, using rules you parse and evaluate yourself.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S4.2/files/`, matching the earlier chapters.

## Build

- Correlation filters matching `CorrelationID`, `Subject` and application properties
- A lexer and recursive-descent parser for a SQL filter subset, like `region = 'au' AND (amount > 100 OR priority IN ('high', 'urgent'))`
- Evaluate the parsed tree against each message
- A default rule that matches everything; a subscription whose rules don't match gets nothing

## Pass when

Table tests cover precedence (`AND` before `OR`), parentheses, strings, numbers, `IN`, `IS NULL` and `NOT`. Messages route only to subscriptions whose rules match. A malformed rule is rejected when it's created, not when a message arrives.

## New in Go

lexers · recursive descent · AST interfaces · type switches

Standard library: `strings` · `strconv` · `unicode` · `fmt`
