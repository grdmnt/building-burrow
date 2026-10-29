# S4.5 · The Real Thing

Point the same scenarios at Azure Service Bus and see how close you got.

> **Tests for this level aren't written yet.** Run `burrow prompt tests` and paste the result to Claude. It will write a hardened suite for this level into `curriculum/levels/S4.5/files/`, matching the earlier chapters.

## Build

- Run the Azure Service Bus emulator in Docker, with queues, topics and subscriptions declared in its `config.json`
- A separate module, `azcompare/`, using the official `azservicebus` Go SDK. This is the only place in the curriculum you add a dependency
- Port your peek-lock, dead-letter, filter and session tests so they run against both Burrow and the emulator
- Write a short table of what matched and what didn't

## Pass when

The same scenario suite passes against Burrow and the emulator, or fails in ways your table explains.

## New in Go

third-party SDKs · separate modules · AMQP concepts

Standard library: `azservicebus (Azure SDK)`
