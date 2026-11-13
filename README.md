# Retain

A self-hosted vocabulary-learning web app and installable PWA for a small group of friends. Learn German from Polish or English from Russian with FSRS spaced repetition. The interface is in English.

## Features

- Nested decks, word editing, and import/export (`retain/v1`, CSV, and school-wordsets import).
- Flashcards, typed answers with language-aware matching, multiple choice, text-to-speech, and undo.
- Optional AI assistance: word enrichment, explanations, mnemonics, and synonym checks. Changes require confirmation.
- Learning analytics, offline reviews with synchronization, friend requests, deck sharing, and optional push reminders.
- Material 3 Expressive-inspired UI with adaptive colors and light/dark themes.

## Stack

Go 1.23+, SQLite, sqlc, and goose on the server; React, TypeScript, Vite, Tailwind, and Dexie in the browser. Production builds embed the frontend into a single Go binary.

## Local development

Requires Go 1.23+, a recent Node.js/npm compatible with Vite 8, and GNU Make.

```sh
# Create the first account; the password is prompted securely.
go run ./cmd/retain admin create --email admin@example.com
make dev
```

Open <http://localhost:5173> and sign in. Frontend dependencies are installed automatically; SQLite defaults to `data/retain.db`, and migrations run automatically. Additional accounts use admin-generated invites.

```sh
make lint test   # Static checks and Go/frontend tests
make build       # Production binary: bin/retain
make gen         # Regenerate database code (requires sqlc)
```

The backend reads environment variables, not `.env` files directly. AI and push are disabled unless configured; see [`.env.example`](.env.example). Never commit API keys or database files.

## Deployment

Docker Compose pulls `ghcr.io/blestafist/retain:latest`, persists SQLite locally, and binds the app to `127.0.0.1:8080` for an existing host Cloudflare Tunnel. See the [deployment guide](deploy/README.md) for setup, administrator creation, and updates. Backups are your responsibility.

Architecture, requirements, and roadmap: [PLAN.md](PLAN.md).
