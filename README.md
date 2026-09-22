# flash-cashback

A cashback campaign service that moves real money. Go backend, PostgreSQL,
Redis, React Native (Expo) mobile app.

## Overview

Users earn cashback on payments, subject to a per-payment minimum, a daily
per-user cap and a total campaign budget, and can redeem their balance.
See [spec/requirements.md](spec/requirements.md) for the full brief.

## Spec

- [spec/presentation.md](spec/presentation.md) — **start here**: overview, trade-offs, and the reasoning behind the current approach.
- [spec/requirements.md](spec/requirements.md) — the brief and derived functional/non-functional requirements.
- [spec/design.md](spec/design.md) — technical design: architecture, data model, concurrency approach, API surface.
- [spec/decisions.md](spec/decisions.md) — resolutions to open questions in the brief, with reasoning.
- [spec/invariants.md](spec/invariants.md) — correctness properties the system must hold under concurrency.
- [spec/api.yaml](spec/api.yaml) — OpenAPI contract for the backend.
- [spec/risks.md](spec/risks.md) — known risks and mitigations/trade-offs.

## Prerequisites

- [Docker](https://www.docker.com/) with Compose v2
- [Go](https://go.dev/) (for running tests locally, outside Docker)
- [Node.js](https://nodejs.org/) and a phone with [Expo Go](https://expo.dev/go), or a simulator, for the mobile app

## Quick start

```sh
cp .env.example .env
make up       # builds and starts postgres, redis, backend
make mobile   # installs mobile deps and starts the Expo dev server
```

Then set `mobile/.env` (see `mobile/.env.example`) to your laptop's LAN IP
so a physical phone can reach the backend.

## Make targets

Run `make` (or `make help`) to list all targets with descriptions.

## Project structure

```
flash-cashback/
├── spec/           # presentation, requirements, design, decisions, invariants, API contract, risks
├── backend/        # Go API (cmd/api, internal/config, internal/httpapi, migrations)
├── mobile/         # Expo (React Native, TypeScript) app
├── scripts/        # demo/load scripts (not yet implemented)
├── docker-compose.yml
├── Makefile
└── .env.example
```

## Status

Backend implemented: cashback earning (daily cap + campaign budget,
concurrency-safe), redemption, ledger, and all API endpoints — see
[spec/presentation.md](spec/presentation.md) for what's built and verified.
Mobile is still just the scaffold's connectivity check.
