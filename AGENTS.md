# AGENTS.md

Instructions for AI agents working in the **Retain** repository.

Retain is a web app + PWA for learning vocabulary with spaced repetition (FSRS), analytics, and an LLM assistant. Built for me and friends (up to ~50 users), self-hosted on a home server behind Cloudflare Tunnel.

**The source of truth is [`PLAN.md`](PLAN.md).** Before starting, read the section relevant to your task. If a decision changes, update `PLAN.md` first, then the code.

## Status

Only the plan exists so far. Next step: stage 0 "Skeleton" (`PLAN.md`, section 16). The commands and paths below describe the target structure: if something doesn't exist yet, create it according to the plan instead of inventing your own.

## Communication

- Talk to the user **in Russian**, briefly. Code, comments, commit messages, docs, and UI strings are in **English**.
- Ask about anything ambiguous instead of guessing — especially the DB schema, public API, the `retain/v1` format, and UX.
- Don't add features beyond the plan without agreement. Put ideas into `PLAN.md` → "Later".

## Stack (summary)

- **Backend**: Go 1.23+, `net/http`, SQLite (`modernc.org/sqlite`, no CGO, WAL), sqlc, goose, `go-fsrs/v3`, `openai-go`, `log/slog`.
- **Frontend**: TypeScript, React, Vite, TanStack Query, Dexie, `ts-fsrs`, Tailwind, Radix Primitives, Motion, `@material/material-color-utilities`, `vite-plugin-pwa`.
- **Deployment**: a single binary with the embedded frontend (`embed.FS`); Docker Compose runs `retain` with local persistent SQLite, while the existing host `cloudflared` systemd service reaches it through a loopback-only port binding.

## Structure

```
cmd/retain/            entry point (server + CLI: `retain admin create ...`)
internal/
  config/ db/ auth/ srs/ lang/ study/ decks/ notes/ importer/ llm/
  jobs/ stats/ sync/ social/ push/ httpapi/
web/                   frontend; web/src/ui — MD3 Expressive UI kit, web/src/theme — tokens, web/src/i18n — strings
testdata/              golden files: fsrs_golden.json, lang_cases.json, import/
```

Details: `PLAN.md`, section 12.

## Commands

```
make dev         # backend + Vite dev server
make gen         # sqlc generate (+ other codegen)
make test        # go test ./... + vitest
make lint        # golangci-lint, eslint, tsc --noEmit
make build       # build frontend and binary (CGO_ENABLED=0)
make llm-smoke   # manual check against the real LLM endpoint (requires LLM_API_KEY)
```

Run `make lint test` before saying "done". If something fails, say so — don't hide it.

## Backend rules

- **Layers**: handler (`httpapi`) → service (`study`, `stats`, ...) → sqlc queries. No business logic or SQL in handlers.
- **SQL**: only via sqlc (`internal/db/queries/*.sql`). Never hand-edit generated code in `internal/db/gen/`. No ORM.
- **Migrations**: goose, embedded via `embed`, applied on startup. Never edit an applied migration — add a new one.
- **Time**: services receive a `clock.Clock`; don't call `time.Now()` directly in business logic. Everything is stored in UTC; a "day" is computed from the user's `timezone` and `day_start_hour`.
- **External dependencies** (LLM, push) sit behind interfaces, with fakes in tests.
- **API errors**: `{"error": {"code": "...", "message": "..."}}`, messages in English. Wrap errors with `fmt.Errorf("...: %w", err)`.
- **Logging**: `slog`, structured. Passwords, tokens, keys, and session contents never go into logs.
- Add new dependencies only when genuinely needed. Prefer the standard library.

## Domain invariants (do not break)

- **`review_logs` is append-only.** Undo sets the `undone` flag instead of deleting the row. A card's state must be reproducible by replaying its logs ordered by `reviewed_at`.
- **The server is the source of truth** for FSRS. Client-side `ts-fsrs` exists only for offline mode and interval previews.
- **Go ↔ TS parity**: any change to FSRS parameters or logic is verified against the shared `testdata/fsrs_golden.json` in both test suites.
- **Idempotency**: an answer's ID is a client-generated UUIDv7, inserted with `ON CONFLICT(id) DO NOTHING`.
- **Progress is per user**: `cards` belong to a `user_id`; notes in shared decks are shared.
- **Import**: adapters only convert an external format into a `Bundle` (`retain/v1`). The core knows nothing about source formats. Re-import never creates duplicates and **never resets FSRS progress**.
- **Answer matching** goes only through `internal/lang` (`de/en/pl/ru` normalizers). A new case is added to `testdata/lang_cases.json` first.

## LLM rules

- OpenAI-compatible endpoint: `LLM_BASE_URL=https://ai.pestit.pl/v1`, default model `cx/gpt-6-luna`.
- **The key lives on the server only.** The frontend reaches the LLM exclusively through our API.
- **The LLM never changes anything without user confirmation**: results always come back as a suggestion.
- Structured output: `json_schema`; on failure fall back to `json_object` + validation in Go + one retry.
- Prompts live in `internal/llm/prompts/*.tmpl`. Any prompt change bumps `prompt_version`, otherwise the cache returns stale answers.
- The app must work when the LLM is unavailable. Tests never call the real endpoint (except `make llm-smoke`).

## Frontend and design rules

- **MD3 Expressive** (`PLAN.md`, section 8). Components are our own, in `web/src/ui/`: Radix for behavior, Tailwind + tokens for appearance, Motion for animation.
- **No hard-coded colors** in components — only `--md-sys-color-*` CSS variables via Tailwind classes. The theme is adaptive: default seed `#3A4527`, user-changeable.
- Rating colors (Again/Hard/Good/Easy) are semantic and independent of the seed (only `harmonize`).
- Animations use springs from `web/src/theme/motion.ts`. Under `prefers-reduced-motion`, use a crossfade instead.
- Mobile-first, touch targets at least 48dp, visible focus, WCAG AA contrast, all navigation keyboard-accessible.
- Server state via TanStack Query, offline data via Dexie. Don't introduce a separate global store unless necessary.
- **The UI language is English.** All UI strings live in `web/src/i18n/en.ts` and are accessed via `t()`; no string literals in components.

## Security

- Passwords: argon2id. Sessions: cookie `HttpOnly; Secure; SameSite=Lax`; the DB stores a hash of the token.
- State-changing requests verify the `Origin` header.
- Every data query is filtered by `user_id` / deck permissions. A test for accessing someone else's resource is required.
- App secrets (`LLM_API_KEY`, VAPID) live only in `.env` and never enter the repo. Existing Cloudflare Tunnel credentials are managed by the host service and never enter the repo. The config example is `.env.example`, without values.
- The client IP comes from `CF-Connecting-IP`. Service ports are never exposed externally.

## Tests

- New logic comes with tests. Table-driven tests in Go, golden files in `testdata/`.
- Must be covered: FSRS (golden), queue and daily limits (time zones), answer normalization, import and re-import, offline log sync, access control.
- Integration tests: `httptest` + a temporary SQLite. FSRS fuzz is disabled or seeded in tests.
- Tests don't depend on the network or the current time.

## Git

- Branch `main`. Small, focused commits; messages in English, Conventional Commits style: `feat(study): ...`, `fix(lang): ...`, `docs: ...`, `chore: ...`.
- Never commit: `.env`, `*.db*`, `data/`, `node_modules/`, `web/dist/`, binaries.
- No `push`, force operations, or history rewriting without an explicit request from the user.
