# Retain — Technical Plan

A web app for learning foreign vocabulary with spaced repetition (FSRS), learning analytics, and an LLM assistant.

- **Format:** web app + PWA (installable on a phone, works offline).
- **Audience:** me and friends (up to ~50 users).
- **Language pairs:** Polish → German (learning German from Polish), Russian → English (learning English from Russian).
- **Hosting:** home server, exposed through Cloudflare Tunnel on our own domain.
- **UI language:** English (content — terms and translations — is in the target and native languages).
- **Design:** Material Design 3 Expressive, adaptive theme: the user picks the accent color; default is swamp green `#3A4527`.

---

## 1. Goals and non-goals

**Goals**
- Quickly add words (with LLM auto-fill) and learn them with flashcards on a smart review schedule.
- See the forgetting curve, progress, and workload forecast.
- Use it on a phone like a native app, including offline.
- Share decks with friends and see each other's progress.

**Non-goals (for now)**
- Payments, public sign-up, scaling to thousands of users.
- Native mobile apps.
- Bundled starter decks (our own decks are imported from exports, section 3.5).

---

## 2. Stack

| Layer | Technology | Why |
|---|---|---|
| Backend language | **Go 1.23+** | Single binary, simple deployment |
| HTTP | `net/http` (routing from 1.22+) | Minimal dependencies |
| Database | **SQLite** via `modernc.org/sqlite` (no CGO), WAL mode | Plenty for dozens of users, zero administration |
| DB access | **sqlc** | Type-safe code from explicit SQL |
| Migrations | **goose** (embedded via `embed`) | Applied on startup |
| Scheduling algorithm | **`github.com/open-spaced-repetition/go-fsrs/v3`** | Official Go implementation of FSRS |
| LLM | **`github.com/openai/openai-go`** with `option.WithBaseURL` | OpenAI-compatible API; default is our own cloud endpoint `https://ai.pestit.pl/v1` |
| Authentication | Cookie sessions (HttpOnly, Secure, SameSite=Lax) + **argon2id** passwords | Simple and secure, no JWT |
| Push notifications | Web Push (VAPID), `github.com/SherClockHolmes/webpush-go` | Review reminders |
| Logging | `log/slog` (JSON) | Standard library |
| Config | Environment variables + `caarlos0/env` | 12-factor |
| Frontend | **TypeScript + React + Vite** | Fast development |
| Client data | TanStack Query + **Dexie** (IndexedDB) | Cache and offline queue |
| Client scheduling (offline) | **`ts-fsrs`** | Same FSRS model as the server |
| Design system | **MD3 Expressive**: tokens from `@material/material-color-utilities` + custom components on **Radix Primitives** + Tailwind | See section 8 |
| Animation | **Motion** (`motion`), spring animations | MD3 Expressive motion scheme |
| Font | **Roboto Flex** (variable) | MD3 Expressive emphasized typography |
| Charts | Recharts (or uPlot for heavy charts) | Curves, heatmap, forecast |
| PWA | `vite-plugin-pwa` (Workbox) | Service worker, manifest, caching |
| Text-to-speech | Web Speech API (`speechSynthesis`) | Free, no server needed |
| Deployment | Docker Compose: app + **cloudflared** + **Litestream** | Home server with no open ports |
| Backups | Litestream → **Cloudflare R2** | Off-site copy |
| CI | GitHub Actions: `go test`, `golangci-lint`, `vitest`, image build | |

The frontend is built into `web/dist` and embedded into the Go binary via `embed.FS`, so we deploy **a single binary**.

---

## 3. Features

### 3.1 MVP
1. **Invite-only accounts**: an admin creates an invite code, a friend signs up via the link.
2. **Decks**: create, rename, delete, language pair (`de`/`pl`, `en`/`ru`). Decks are **nested**: group → decks (e.g. class → textbook chapters); you can study a single chapter or the whole group at once.
3. **Words (notes)**: term (word or phrase) and translation are required; transcription, example, note, tags, and language-specific fields (article, plural, case government, etc.) are optional. The model is universal, see section 3.5.
   - Each note generates cards: `forward` (term → translation) and, optionally, `reverse` (translation → term).
4. **Review session**:
   - Queue: overdue reviews → learning cards → new cards (respecting daily limits). For a parent deck — across all children.
   - Show card → "Show answer" → ratings **Again / Hard / Good / Easy**. Each button shows the interval until the next review ("10m", "3d", "2mo").
   - Hotkeys (Space, 1–4), swipes on mobile, undo of the last answer.
5. **Exercise modes**:
   - Card flip (classic).
   - Typed answer: language-aware normalization (section 3.4) + Levenshtein distance → suggested rating (exact → Good, typo → Hard, wrong → Again).
   - Multiple choice of 4 (distractors from the same deck) — for new cards.
6. **Text-to-speech** for the term, on demand and automatically (configurable).
7. **Import** in the universal `retain/v1` format and via adapters (school `classes/wordsets` export, CSV): preview before applying, re-import without losing progress (section 3.5). **Export** to `retain/v1` and CSV.
8. **Settings**: new cards per day, max reviews per day, desired retention (default 0.90), learning steps (`1m 10m`), time zone, start of a "new day" (default 04:00), appearance: accent color, scheme style, contrast, light/dark/system theme (section 8.1).
9. **Basic stats**: reviewed/learned today, streak, cards due.

### 3.2 Version 1.0
- **LLM assistant** (section 6): word auto-fill, bulk deck enrichment, questions about a word, mnemonics, synonym checking.
- **Full analytics** (section 5).
- **Offline PWA**: review without a connection, sync when back online.
- **Push reminders**: "You have 34 cards to review" at a chosen time.
- **Shared decks**: only owners can share, and only with accepted friends; sharing a deck includes its descendant subtree. `copy` makes an independent, editable copy with fresh per-user progress. `subscribe` gives read-only access to the source notes, live content updates, and separate per-user cards/progress; subscribers cannot re-share. The owner can revoke a subscription and the recipient can unsubscribe. Revocation/removing a friendship removes live subscription access and syncs tombstones to the recipient; progress remains private on the server and is available again only if access is re-granted. Independent copies remain unaffected.
- **Friends**: explicit friend requests by email with acceptance/decline and removal. Do not reveal whether an arbitrary email has an account. Activity and leaderboard stats are visible only to accepted friends when the user opts in; show aggregate counts only, never terms, examples, deck names, or individual review details. Weekly leaderboards use Monday 00:00–Monday 00:00 UTC, count active reviews and newly matured forward notes (first stability ≥ 21 days), show current streak, and rank by reviews then learned notes then streak.
- **Leeches**: cards with 8+ lapses are flagged; the LLM suggests a mnemonic and an extra example.
- **Search and filters** by term, tag, card state.

### 3.3 Later
- Anki `.apkg` import adapter (a zip with SQLite inside).
- Per-user FSRS parameter optimization (section 4.4).
- Text-to-speech via OpenAI-compatible `/audio/speech` if system voices are lacking.
- Images on words.

### 3.4 Language specifics

Card: front — term in the target language (`de`, `en`), back — translation in the native language (`pl`, `ru`). Reverse cards are enabled in deck settings.

**Answer normalization and rating suggestion in typing mode** (package `internal/lang`, one matcher per language):

| Language | Rules |
|---|---|
| Common | Unicode NFC, trim, lowercase, collapse whitespace, strip punctuation. A translation may contain several variants separated by `;` — any of them is accepted. Plus an `alternatives` list (accepted synonyms). |
| `de` | The `der/die/das` article of nouns is checked separately: a correct noun without an article or with a wrong article gets the deck's `wrong_article_rating` (default: Hard; configurable to Again). `ß` = `ss`, `ä/ö/ü` = `ae/oe/ue` (for users without a German layout). Noun capitalization is ignored. |
| `en` | `to` before verbs and articles `a/an/the` are ignored. British and American spellings are handled via `alternatives` (filled in by the LLM). |
| `pl` | An answer without diacritics (`ą ć ę ł ń ó ś ź ż`) → Hard (almost right), not Again. |
| `ru` | `ё` = `е`, stress marks (U+0301) are removed. |

The matcher returns a suggestion, not a submitted review: an exact accepted answer suggests **Good**, one Levenshtein edit suggests **Hard**, and other mismatches suggest **Again**. The learner may choose any rating. A German article-only mistake uses the deck setting when the noun stem matches exactly; a stem typo uses the normal **Hard** suggestion. Language, direction, expected answer, alternatives, and article policy come from the authenticated user's server-side card, deck, and note.

- **On-screen character bar** in typing mode: `ä ö ü ß` for German, `ą ć ę ł ń ó ś ź ż` for Polish.
- **TTS voices**: `de-DE`, `en-GB`/`en-US`, `pl-PL`, `ru-RU`. Voice availability is checked via `speechSynthesis.getVoices()`; if there is no voice, the button is hidden.

### 3.5 Universal storage and import

Internally everything lives in a single model: **collection → decks (nested) → notes → cards**. External exports are converted into it by adapters or by your own converter. The app core knows nothing about source formats.

**Note model**
- `term` — the term (word or phrase), `translation` — the translation. These are the only required fields.
- Optional fields: `transcription`, `example`, `example_translation`, `notes`, `tags`, `alternatives` (additional accepted answers).
- `fields` — free-form JSON for language data: `kind` (word/phrase), `pos`, `article`, `plural`, `governs` (case: A/D/G), `reflexive`, `verb_forms`, `cefr`, `mnemonic`, and anything needed later. The DB schema does not change for this.
- `external_id` — ID in the source system (if any), so a re-import updates instead of duplicating.
- `term_key` — normalized term (lowercase, NFC, collapsed whitespace), used for duplicate detection.

**Exchange format `retain/v1`** — the target format for converters and also the export format. The JSON Schema is served at `/schema/retain-v1.json`.

```json
{
  "format": "retain/v1",
  "source": "school-wordsets",
  "decks": [
    {
      "external_id": "class:26083",
      "name": "3C 2026/2027 · Ein tolles Team 3",
      "lang_target": "de",
      "lang_native": "pl",
      "decks": [
        {
          "external_id": "wordset:114543",
          "name": "Kapitel 12 · Berlin",
          "expected_count": 65,
          "notes": [
            { "term": "der Drachenflieger", "translation": "paralotnia",
              "fields": { "kind": "word", "pos": "noun", "article": "der" } },
            { "term": "berichten über A", "translation": "donisić o, informować o",
              "alternatives": ["donisić o", "informować o"],
              "fields": { "kind": "word", "pos": "verb", "governs": "A" } },
            { "term": "ich bin nicht einverstanden", "translation": "nie zgadzam się",
              "fields": { "kind": "phrase" } }
          ]
        }
      ]
    }
  ]
}
```

- Decks can be nested to any depth. `lang_*` is inherited from the parent.
- Only `format`, `decks[].name`, `notes[].term`, and `notes[].translation` are required. Everything else may be omitted: the LLM can fill empty fields.

**Adapters** (`internal/importer/adapters/`)
```go
type Adapter interface {
    Name() string
    Detect(raw []byte) bool            // format auto-detection
    Parse(raw []byte) (*Bundle, error) // → in-memory retain/v1
}
```
- `retain` — the native format.
- `school-wordsets` — an export shaped like `classes[].class / wordsets[].info / entries[]`: `class` → parent deck (`external_id = class:<group_id>`), `wordset` → child deck (`wordset:<info.id>`, `expected_count`), `entries` → notes. If the word count does not match `expected_count`, a warning is shown. The file has no languages, so the pair is chosen in the preview (default de/pl).
- `csv` — `term;translation;example;tags`.
- Later: Anki `.apkg`.

If you convert files yourself, emitting `retain/v1` is enough — no adapter needed. `school-wordsets` is built in for convenience, so you don't have to run a converter by hand.

**Enrichment on import** (`internal/lang`, no LLM, deterministic; results go into `fields`)
- `de`: `der/die/das` + capitalized word → `pos=noun`, `article`. Trailing ` A` / ` D` / ` G` → `governs` (`berichten über A`). `sich …` → `reflexive`. 3+ words without an article → `kind=phrase`.
- Translation: parts separated by `,` or `;` → `alternatives`. Parenthetical notes (`powstanie (np. zbrojne)`) go into `notes` and are not required when typing. A trailing period (`Mam propozycję.`) is stripped.
- Typed answers: the `A/D/G` markers need not be typed, the preposition is required. The article is checked by the rules in section 3.4.
- Typos in the source (`donisić`) are not silently fixed. The LLM can flag them; the user decides.

**Import flow**
1. `POST /import/preview`: the file is stored in `import_batches` along with the raw source (gzip), the adapter is auto-detected. The response contains the deck tree and per-deck counts: new / changed / unchanged / warnings.
2. The user picks the language pair and where to put the decks, then `POST /import/{batch_id}/apply`. Everything is applied in a single transaction.
3. **Re-import** (the teacher added words to a chapter): a deck is matched by `(owner, source, external_id)`, a note by `external_id`, falling back to `term_key`.
    - New words are added.
    - Changed words get their translation updated; **FSRS progress is preserved**.
    - Words that disappeared from the source are kept by default. Option: suspend them.
    - Matching soft-deleted decks/notes/cards are restored rather than duplicated; their card progress is preserved.
4. Sources stay in `import_batches`, so they can be re-parsed with an improved adapter without re-uploading.
5. After import you can run "Enrich with LLM" for empty fields.

---

## 4. Learning algorithm (FSRS)

### 4.1 Model
FSRS (Free Spaced Repetition Scheduler) is a DSR memory model. Each card stores:
- **S (stability)** — the number of days until recall probability drops to 90%.
- **D (difficulty)** — card difficulty, 1..10.
- **R (retrievability)** — the probability of recall right now; computed, not stored.

Forgetting curve (FSRS-4.5/5):

```
R(t, S) = (1 + FACTOR * t / S) ^ DECAY,   DECAY = -0.5,  FACTOR = 19/81
```
where `t` is days since the last review. At `t = S`, `R = 0.9`.

The next interval is derived from the desired retention `r_d` (default 0.9):
```
I(r_d, S) = S / FACTOR * (r_d ^ (1/DECAY) - 1)
```

### 4.2 Card states
```
New ──(first answer)──▶ Learning ──(steps done)──▶ Review
                            ▲                        │
                            └──── Relearning ◀───(Again)
```
- **Learning/Relearning** — short intra-day steps (`1m, 10m`).
- **Review** — long FSRS intervals.
- Interval **fuzz** (built into go-fsrs) so cards don't pile up on the same day.

### 4.3 Implementation
- Package `internal/srs` — a thin wrapper over `go-fsrs`:
  ```go
  type Scheduler interface {
      Preview(card Card, now time.Time) map[Rating]Card // intervals for the 4 buttons
      Review(card Card, rating Rating, now time.Time) (Card, ReviewLog)
      Retrievability(card Card, now time.Time) float64
  }
  ```
- Parameters (weights `w[]`, `request_retention`, `maximum_interval`, `enable_fuzz`) are stored in `user_settings`; default FSRS weights out of the box.
- **The server is the source of truth.** Every answer is written to `review_logs` (append-only). A card's state can be recomputed by replaying its logs in order — this underpins offline sync and undo.
- The client runs `ts-fsrs` with the same parameters — for offline mode and instant interval previews.
- **Cross-test**: a golden file of answer sequences; the Go and TS implementations must produce identical S/D/due (tolerance 1e-6).

### 4.4 Personal optimization (later)
- Once a user has 1000+ reviews, an "Optimize" button exports `review_logs` and runs the official optimizer (`fsrs-rs` CLI or Python `fsrs-optimizer`) as a separate process/container.
- New weights are stored with a version; before/after metrics are shown (log loss, RMSE).

---

## 5. Analytics ("learning curve")

All metrics are computed from `review_logs` and `cards` with SQL; heavy aggregates are cached for 5 minutes.

| Chart / metric | What it shows | Source |
|---|---|---|
| **Card forgetting curve** | R(t) from the last review to the next, with points for past answers | card S + its logs |
| **Average deck curve** | How well a deck's words hold up on average | median S per deck |
| **Actual vs desired retention** | % correct on Review cards per week vs 90% | `review_logs` |
| **Workload forecast** | Cards due per day for the next 30 days | `cards.due` |
| **Activity heatmap** | Reviews per day over a year | `review_logs` |
| **State distribution** | New / Learning / Review / Relearning; "young" (S < 21d) and "mature" (S ≥ 21d) | `cards` |
| **Vocabulary growth** | Words "learned" (S ≥ 21d) over time, per language | `review_logs` |
| **S and D distribution** | Stability and difficulty histograms | `cards` |
| **Answer time** | Average time per card, total time per day | `review_logs.duration_ms` |
| **Leeches** | Top problem words by lapse count | `cards.lapses` |
| **"Words I know right now"** | Σ R(now) over all Review cards | computed in Go |

---

## 6. LLM assistant (OpenAI-compatible API)

### 6.1 Connection
- Package `internal/llm`, `openai-go` client with a configurable `base_url`:
  ```
  LLM_BASE_URL=https://ai.pestit.pl/v1        # cloud OpenAI-compatible endpoint
  LLM_API_KEY=...                             # Bearer token (the endpoint returns 401 without it)
  LLM_MODEL=cx/gpt-6-luna                     # default model
  LLM_MODEL_FAST=                             # optional: a different model for bulk deck enrichment (defaults to LLM_MODEL)
  LLM_TIMEOUT=30s
  LLM_DAILY_LIMIT_PER_USER=300                # requests per day
  ```
- The key lives **only on the server**; the client reaches the LLM through our API.
- If `LLM_BASE_URL` is not set, all LLM features are hidden in the UI and the app works without them.
- On startup the server calls `GET /v1/models` and checks that `LLM_MODEL` is listed; if not — a warning in the logs and in the admin panel. The model can be switched in the admin panel without a restart (the list comes from `/v1/models`, the choice is stored in `app_settings`).
- Requests to the endpoint are outbound, so Cloudflare Tunnel doesn't interfere. Retries with backoff on 429/5xx, plus a circuit breaker: if the LLM is unavailable, LLM buttons are temporarily disabled and the rest of the app keeps working.

### 6.2 Features
| Feature | When | Returns |
|---|---|---|
| **Word auto-fill** | When adding a word — a "Fill" button or automatically after typing | Translation variants, transcription (IPA), part of speech, article and plural for `de` nouns, principal parts for verbs, 1–2 examples with translation, CEFR level, `alternatives` |
| **Deck enrichment** | After import or on demand | Background job: empty fields are filled in batches of 20 words, progress shown in the UI |
| **Ask about a word** | Button on a card / word page | Streaming chat answer (SSE): synonym differences, usage, grammar |
| **Mnemonic** | For leeches | 2–3 association ideas in the user's native language |
| **Synonym check** | In typing mode when the answer didn't match on a forward card — a "Is my answer also correct?" button | Verdict + explanation; only after explicit confirmation is the answer added to `alternatives`. The learner chooses the rating separately. |

### 6.3 Rules
- **The LLM never changes anything without confirmation**: results are shown as a suggestion that the user accepts in full or per field. Synonym checks are read-only; accepting a positive verdict is a separate action that adds the answer to `alternatives`.
- **Structured output**: `response_format: json_schema`. If the provider doesn't support it — fall back to `json_object`, validate in Go, and retry once on invalid JSON.
- **Prompts** live in `internal/llm/prompts/*.tmpl` (embedded) and account for the language pair; words with multiple meanings return a list.
- **Cache**: auto-fill responses are cached in `llm_cache` keyed by `sha256(model + prompt_version + lang_pair + word)` — the same word from different friends isn't paid for twice.
- **Limits**: daily per-user limit (`llm_usage`), token accounting for cost stats in the admin panel.
- **SSE through Cloudflare**: `Content-Type: text/event-stream`, a heartbeat comment every 15 s to avoid hitting the 100 s timeout.

---

## 7. Data model (SQLite)

```sql
users(
  id INTEGER PK, email TEXT UNIQUE, name TEXT, password_hash TEXT,
  is_admin BOOL, created_at)

invites(code TEXT PK, created_by → users, used_by → users NULL, expires_at, created_at)

sessions(token_hash BLOB PK, user_id → users, expires_at, user_agent, created_at)

user_settings(
  user_id PK → users,
  new_per_day INT DEFAULT 20, max_reviews_per_day INT DEFAULT 200,
  request_retention REAL DEFAULT 0.9, maximum_interval INT DEFAULT 36500,
  learning_steps TEXT DEFAULT '1m 10m', relearning_steps TEXT DEFAULT '10m',
  fsrs_weights TEXT /* JSON */, timezone TEXT, day_start_hour INT DEFAULT 4,
  reminder_time TEXT NULL, tts_auto BOOL,
  theme_mode TEXT DEFAULT 'system', theme_seed TEXT DEFAULT '#3A4527',
  theme_variant TEXT DEFAULT 'fidelity', theme_contrast REAL DEFAULT 0)

app_settings(key TEXT PK, value TEXT /* JSON */, updated_at)   -- e.g. llm_model

decks(
  id INTEGER PK, owner_id → users, parent_id → decks NULL, -- nesting
  name, description, position INT,
  lang_target TEXT,                  -- 'de' | 'en' (NULL → inherited from parent)
  lang_native TEXT,                  -- 'pl' | 'ru'
  reverse_cards BOOL DEFAULT 0,
  wrong_article_rating INT DEFAULT 2, -- Hard
  color_seed TEXT NULL,              -- per-deck accent color
  source TEXT NULL, external_id TEXT NULL, expected_count INT NULL,
  source_deck_id NULL,               -- if copied from a friend
  created_at, updated_at, deleted_at NULL, deletion_batch TEXT NULL)
   UNIQUE(owner_id, source, external_id), INDEX(parent_id)

friend_requests(id PK, sender_id → users, recipient_id → users,
  status TEXT /* pending|accepted|declined|cancelled */, created_at, updated_at)
friendships(user_a_id → users, user_b_id → users, created_at) -- canonical user_a_id < user_b_id
social_settings(user_id PK → users, visible BOOL DEFAULT 0)
deck_shares(id PK, source_deck_id → decks, recipient_id → users,
  mode TEXT /* copy|subscribe */, status TEXT /* active|revoked|unsubscribed|complete */,
  copied_deck_id → decks NULL, created_at, updated_at)

notes(                               -- a word or phrase
  id INTEGER PK, deck_id → decks,
  term TEXT NOT NULL, term_key TEXT NOT NULL, -- term_key: normalized, for duplicates
  translation TEXT NOT NULL, transcription, example, example_translation, notes,
  tags TEXT /* JSON */, alternatives TEXT /* JSON */,
  fields TEXT /* JSON: kind, pos, article, plural, governs, reflexive, verb_forms, cefr, mnemonic, ... */,
  external_id TEXT NULL, import_batch_id → import_batches NULL,
  created_at, updated_at, deleted_at NULL, deletion_batch TEXT NULL)
   UNIQUE(deck_id, term_key), UNIQUE(deck_id, external_id)

import_batches(
  id TEXT PK, user_id → users, adapter TEXT, filename TEXT,
  raw BLOB /* gzipped source */, raw_sha256 BLOB,
  status TEXT /* previewed|applied|failed */, summary TEXT /* JSON: counts, warnings */,
  created_at, applied_at NULL)

cards(
  id INTEGER PK, note_id → notes, user_id → users,
  direction TEXT /* forward|reverse */,
  state INT /* 0 New,1 Learning,2 Review,3 Relearning */,
  due DATETIME, stability REAL, difficulty REAL,
  elapsed_days INT, scheduled_days INT, step INT,
  reps INT, lapses INT, last_review DATETIME NULL,
   suspended BOOL, is_leech BOOL, updated_at, deleted_at NULL, deletion_batch TEXT NULL)
  INDEX(user_id, due), INDEX(note_id)

review_logs(                         -- append-only
  id TEXT PK /* UUIDv7 from the client — idempotency */,
  card_id → cards, user_id → users,
  rating INT /* 1..4 */, mode TEXT /* flip|typing|choice */,
  state_before INT, stability_before REAL, difficulty_before REAL,
  stability_after REAL, difficulty_after REAL, scheduled_days INT,
  reviewed_at DATETIME /* client time */, received_at DATETIME,
  duration_ms INT, undone BOOL DEFAULT 0)
  INDEX(user_id, reviewed_at), INDEX(card_id, reviewed_at)

llm_cache(key BLOB PK, model TEXT, prompt_version INT, response TEXT /* JSON */, created_at)

llm_usage(id PK, user_id → users, kind TEXT, model TEXT,
  prompt_tokens INT, completion_tokens INT, cached BOOL, created_at)
  INDEX(user_id, created_at)

jobs(id TEXT PK, user_id → users, kind TEXT /* deck_enrich */, payload TEXT,
  status TEXT /* queued|running|done|failed */, progress INT, total INT,
  error TEXT NULL, created_at, updated_at)

push_subscriptions(id PK, user_id → users, endpoint UNIQUE, p256dh, auth, created_at)
```

Cards are tied to `user_id`: when subscribed to a friend's deck, notes are shared but progress is per user.

Deleting a deck soft-deletes its entire descendant subtree, notes, and cards. A private `deletion_batch` marker groups rows changed by one cascade so restoring a deck only restores the exact subtree rows deleted with it, never independently deleted records. Soft-deleted rows retain their unique identities; re-importing a matching source/external ID restores existing deck/note/card rows instead of creating duplicates, preserving card progress.

Background jobs (`jobs`) are run by a simple in-process worker pool; on restart, jobs in `running` status go back to the queue.

---

## 8. Design: Material Design 3 Expressive

### 8.1 Color: adaptive theme
The entire palette is built at runtime from a single **seed color** (MD3 dynamic color). Change the accent and the whole app changes.

- **Default**: swamp green **`#3A4527`**.
- **Settings → Appearance**:
  - Preset swatches (MD3 color swatches): swamp `#3A4527` (default), plus 6–8 curated hues.
  - Custom color: color picker + HEX field.
  - **Scheme style**: `Fidelity` (default — stays closest to the chosen hue), `TonalSpot` (calmer), `Vibrant`, `Expressive` (rotates hue more, brighter), `Monochrome`.
  - **Contrast**: standard / medium / high (`contrastLevel` 0 / 0.5 / 1).
  - Theme: light / dark / system.
  - Live preview (card, rating buttons, chart) before saving.
- **Deck color** (optional): a deck can have its own seed; during a session for that deck the theme recolors to it. Helps tell German and English apart at a glance.
- **Generation**: `@material/material-color-utilities` (`Hct`, `SchemeFidelity`/`SchemeTonalSpot`/..., `DynamicScheme`, `MaterialDynamicColors`) right in the browser → CSS variables `--md-sys-color-*` (primary, on-primary, primary-container, secondary, tertiary, surface, surface-container-lowest…highest, outline, error, etc.) on `:root`. Recalculation takes < 5 ms.
- **No flash on load**: the last computed palette is cached in `localStorage`; an inline script in `index.html` applies it before React renders. Without a cache — a built-in default palette generated at build time (`web/scripts/gen-theme.ts`).
- **Sync**: `theme_seed`, `theme_variant`, `theme_contrast`, `theme_mode` are stored in `user_settings` and follow the user across devices.
- The Tailwind theme references variables only (`bg-primary-container`, `text-on-surface`, etc.); **no hard-coded colors in components** (enforced by a lint rule).
- **Semantic colors don't depend on the seed**: rating buttons use fixed hues, harmonized (`Blend.harmonize`), so Again always stays "red" and Good "green" with any accent: Again → error, Hard → orange, Good → green, Easy → blue, each in its own `container` variant.
- **Charts**: series use `primary` / `tertiary` / `secondary` and semantic roles.
- `theme-color` in `<meta>` updates along with the palette (status bar color on Android).
- **Accessibility**: when a custom color is chosen, contrast of `on-*`/`*` pairs is checked against WCAG AA; if it fails — a warning and an offer to raise contrast. Tests run all presets × styles × light/dark.

### 8.2 Typography
- **Roboto Flex** (variable, self-hosted via `@fontsource-variable`) covering Latin with diacritics and Cyrillic.
- MD3 type scale (display / headline / title / body / label) + Expressive **emphasized** styles for key spots: the term on a card — `display-large-emphasized`, session summary and streak — `headline-emphasized`.

### 8.3 Shape
- MD3 corner scale with Expressive's extended values (`extra-large-increased`, `extra-extra-large`, etc.) as CSS tokens `--md-sys-shape-corner-*`.
- **Shape morphing**: buttons change corner radius on press (round → square), selected items change shape.
- Expressive abstract shapes (cookie, clover, sunny, ...) — SVG for friend avatars, the streak badge, and the loading indicator.

### 8.4 Motion
- Expressive spring-based **motion scheme**: spatial springs (position, size, shape) and effects springs (color, opacity), each in fast / default / slow variants. Parameters are tokens in `web/src/theme/motion.ts`, implemented with Motion.
- Card flip, card fly-out after answering, next card entrance — spring animations.
- `prefers-reduced-motion` → animations are replaced by a crossfade.
- Haptic feedback via `navigator.vibrate` on Android when rating.

### 8.5 Components and layout
| Place | MD3 Expressive component |
|---|---|
| 4 rating buttons | **Connected button group** |
| Add word / import / deck | **FAB menu** |
| Session progress | **Wavy linear progress indicator** |
| Loading, waiting for the LLM | **Loading indicator** (shape morphing) |
| Session actions (undo, TTS, edit, ask LLM) | **Floating toolbar** |
| "Study" + mode picker on a deck | **Split button** |
| Navigation | **Navigation bar** (compact) / **navigation rail** (≥ 840px) |
| Undo, errors | Snackbar |
| LLM suggestions | Bottom sheet (mobile) / side sheet (desktop) with per-field acceptance |

- Window size classes: compact < 600, medium 600–839, expanded ≥ 840.
- Touch targets at least 48dp, visible focus ring, WCAG AA contrast (generated palette checked in tests).

### 8.6 Implementation
- There is no official web component library for MD3 Expressive at planning time (`@material/web` is in maintenance mode and lacks Expressive). So we build a small UI kit in `web/src/ui/`:
  behavior and accessibility — **Radix Primitives**, appearance — Tailwind + MD3 tokens, animation — Motion.
- A `/dev/ui` page (dev only) — a showcase of all components in both themes.

### 8.7 UI language
**English.** All UI strings live in `web/src/i18n/en.ts` (a typed object, key → string) and are accessed through a `t()` helper; no string literals in components. Dates and numbers are formatted via `Intl` with the `en` locale. Other locales can be added later without touching components.

---

## 9. API (REST, JSON, `/api/v1`)

Routes below are relative to `/api/v1`. Invite codes are single-use and valid for 7 days. Sessions last 30 days. Passwords must be at least 12 characters long.

```
POST   /auth/register            {invite, email, name, password}  (creates account; no session or Set-Cookie)
POST   /auth/login               {email, password}       → Set-Cookie
POST   /auth/logout
GET    /me                       → profile + full settings
PATCH  /me/settings              partial flat snake_case JSON → full settings

POST   /admin/invites            → {code, url}           (admin only; url: /signup?invite=...)
GET    /admin/llm-usage          token usage per user
GET    /admin/llm/models         model list from the endpoint (/v1/models)
PUT    /admin/llm/model          {model} — switch model without restart

GET    /decks                    deck tree + counts (new/learning/due, including children)
POST   /decks
PATCH  /decks/{id}
DELETE /decks/{id}
POST   /import/preview           multipart: file (+ adapter if not detected) → {batch_id, tree, counts, warnings}
POST   /import/{batch_id}/apply  {lang_target, lang_native, parent_deck_id?, on_missing: keep|suspend}
GET    /import/batches           import history
GET    /schema/retain-v1.json    JSON Schema of the exchange format
GET    /decks/{id}/export?format=retain|csv   (including child decks)
POST   /decks/{id}/share         {user_id, mode}
POST   /decks/{id}/enrich        → {job_id}  fill empty fields via LLM

GET    /decks/{id}/notes?q=&tag=&state=&cursor=
POST   /decks/{id}/notes         creates a note + cards
PATCH  /notes/{id}
POST   /notes/{id}/alternatives {alternative}  adds one accepted answer idempotently
DELETE /notes/{id}

POST   /llm/enrich               {deck_id, word}         → field suggestions
POST   /llm/ask                  {note_id, question}     → SSE stream
POST   /llm/mnemonic             {note_id}
POST   /llm/check-answer         {card_id, answer}       → {accepted, explanation}

GET    /jobs/{id}                background job status and progress

GET    /study/queue?deck_id=&limit=50   cards + interval previews
POST   /study/queue/next               {deck_id, limit, exclude_card_ids} next batch, excluding cards already in this session
POST   /study/choice-options      {card_id} → {choices: [4 strings]} (owned active New card only; one correct answer plus three distinct distractors from the same actual deck and direction, excluding accepted answers; 409 if unavailable)
POST   /study/check-answer        {card_id, answer, mode?: typing|choice} → {match: exact|typo|article|wrong, suggested_rating} (default mode: typing; choice is exact=Good, otherwise Again)
POST   /study/review             {id(uuid), card_id, rating, mode, reviewed_at, duration_ms}
POST   /study/undo               {review_id}
POST   /cards/{id}/suspend | /unsuspend | /reset

POST   /sync                     {reviews:[...], since} → {applied, rejected, changed_cards, changed_notes, cursor, has_more}

GET    /stats/overview           streak, today, totals
GET    /stats/forecast?days=30
GET    /stats/retention?period=week
GET    /stats/heatmap?year=
GET    /stats/cards/{id}/curve
GET    /stats/distribution

GET    /friends/activity
GET    /friends/leaderboard?period=week
GET    /friends
POST   /friends/requests         {email}
GET    /friends/requests         incoming and outgoing requests
POST   /friends/requests/{id}/accept | /decline
DELETE /friends/{user_id}
GET    /social/settings
PATCH  /social/settings          {visible}
GET    /decks/{id}/shares
DELETE /decks/{id}/shares/{share_id}  revoke (owner) or unsubscribe (recipient)

GET    /push/vapid-key
POST   /push/subscribe | DELETE /push/subscribe
```

After registration, the user logs in. The invite URL is a same-origin relative URL.

Errors: `{"error": {"code": "not_found", "message": "..."}}` (messages in English). CSRF protection: SameSite cookie + `Origin` header check against the configurable canonical origin `PUBLIC_ORIGIN`. A state-changing request's `Origin` must match the full scheme and normalized host/effective port; never derive the scheme from untrusted forwarded headers. Development with Vite uses `http://localhost:5173`; production uses the externally exposed HTTPS origin behind Cloudflare Tunnel.

---

## 10. Offline and sync

1. The service worker precaches the app shell and uses stale-while-revalidate only for safe API GET requests; authenticated and mutation responses are never cached.
2. At session start the client fetches the queue (`/study/queue`) with a day's buffer and stores cards and notes in IndexedDB.
3. Offline answer: `ts-fsrs` computes the new state locally → the card is updated in IndexedDB → a log entry with a UUIDv7 and `reviewed_at` goes into the durable `outbox`.
4. When online, `POST /sync` sends a bounded batch of logs and an optional opaque `since` cursor. The server inserts logs idempotently (`ON CONFLICT(id) DO NOTHING`). If older logs arrive from another device, it recomputes each affected card by replaying active logs ordered by `reviewed_at` and returns the authoritative card state. A permanently invalid/inaccessible review is returned in `rejected` without blocking feed pagination; the client retains it in the outbox, marks it for attention, and only deletes it after an explicit user action.
5. Server changes are delivered through an append-only `sync_events` log with a globally monotonic sequence and an immutable, never-reused per-account owner-scope ID. Numeric user IDs can be reused after account deletion, so they are not sufficient as the feed isolation key. Events include current note/card snapshots and tombstones; authorization loss must produce a tombstone so clients remove data they can no longer access. The client stores the scope ID with its offline owner marker so a reused numeric user ID cannot inherit old IndexedDB data or outbox entries.
6. The `since` value is an opaque, versioned HMAC-signed cursor bound to the authenticated user and its owner scope. The signing key is generated once and persisted in `app_settings`; it is not a new required environment secret. Each response has `cursor` and `has_more`; clients apply every page and continue until there are no more events and the cursor stabilizes. Each page's state is applied atomically in IndexedDB before advancing the locally stored cursor.
7. The client applies server-authoritative card snapshots only when their feed sequence is not older than the cached snapshot, then removes acknowledged outbox entries. A failed request or page application must leave pending reviews and the prior cursor intact for safe retry. Pending and rejected outbox entries survive logout and account switches, remain bound to their immutable account scope, and are only deleted after server acknowledgement or an explicit user action.
8. Word edit conflicts use last-write-wins by `updated_at`. LLM features are unavailable offline — buttons are disabled with a hint.

The initial sync must also deliver currently active notes and cards that existed before `sync_events` was introduced. Stage 8 uses one next-sequential migration (`00009` after the current `00008`) for the event log, capture triggers, and initial snapshot; do not leave a migration-number gap.

---

## 11. Reminders

- A background goroutine (`time.Ticker` once a minute) finds users whose `reminder_time` has arrived in their time zone and who have cards due → Web Push.
- At most one push per day; subscriptions for which the push service returns 410 Gone are deleted.
- Push requests are outbound, so Cloudflare Tunnel doesn't interfere.
- On iOS, Web Push works only for a PWA added to the home screen (iOS 16.4+) — the UI says so.

---

## 12. Project structure

```
retain/
├── cmd/retain/main.go            # startup: config, DB, migrations, HTTP, workers
├── internal/
│   ├── config/
│   ├── db/
│   │   ├── migrations/*.sql      # goose
│   │   ├── queries/*.sql         # sqlc
│   │   └── gen/                  # generated code
│   ├── auth/                     # argon2id, sessions, middleware
│   ├── srs/                      # go-fsrs wrapper, card recomputation from logs
│   ├── lang/                     # de/en/pl/ru normalizers, answer matching, term parsing
│   ├── study/                    # queue, limits, review, undo
│   ├── decks/  notes/            # CRUD, deck tree, export
│   ├── importer/                 # preview/apply, upsert by external_id/term_key
│   │   └── adapters/             # retain, school-wordsets, csv, (apkg)
│   ├── llm/                      # client, prompts (embedded), cache, limits, SSE
│   ├── jobs/                     # background jobs (deck enrichment)
│   ├── stats/                    # analytics
│   ├── sync/                     # offline sync
│   ├── social/                   # sharing, leaderboard, activity
│   ├── push/                     # VAPID, reminder worker
│   └── httpapi/                  # routes, handlers, errors, middleware
├── web/                          # React + Vite + TS
│   ├── scripts/gen-theme.ts      # default palette at build time
│   ├── src/
│   │   ├── theme/                # dynamic color from seed; tokens: typography, shape, motion
│   │   ├── ui/                   # MD3 Expressive UI kit (Radix + Tailwind + Motion)
│   │   ├── i18n/                 # en.ts — all UI strings, t()
│   │   ├── pages/  components/
│   │   ├── api/  db/  srs/  sw/
│   └── dist/                     # embedded via embed
├── testdata/
│   ├── fsrs_golden.json          # shared test vectors for Go and TS
│   ├── lang_cases.json           # answer normalization cases
│   └── import/                   # sample exports for adapter tests
├── sqlc.yaml
├── Dockerfile
├── docker-compose.yml            # retain + cloudflared + litestream
├── Makefile                      # dev, gen, test, lint, build
├── AGENTS.md
└── PLAN.md
```

Thin handlers → services (`study`, `stats`, `llm`, ...) → sqlc repositories. Services take a `clock.Clock` (time substitution in tests); `llm.Client` is an interface (faked in tests).

---

## 13. Frontend screens

1. **Sign in / sign up via invite**.
2. **Home**: decks with counts (new / learning / due), a "Study" split button, streak, FAB menu.
3. **Session**: card, wavy progress, connected button group with intervals, floating toolbar (undo, TTS, edit, ask LLM), session summary at the end.
4. **Deck**: word list, search, filters, quick add (Enter → LLM auto-fill → confirm → next word), import/export, "Enrich with LLM".
5. **Word**: fields, answer history, forgetting curve, S/D/R, "Ask about this word" chat.
6. **Stats**: charts from section 5.
7. **Friends**: activity, leaderboard, shared decks.
8. **Settings**: limits, retention, steps, reminders, time zone; **Appearance** — color presets, color picker, scheme style, contrast, theme, live preview.

Mobile-first; swipe left = Again, right = Good.

---

## 14. Testing

- **Unit (Go)**: `srs` — golden vectors and edge cases (first answer, Again on Review, maximum interval); `study` — queue and daily limits with time zones and `day_start_hour`; `lang` — the case table from `lang_cases.json` (articles, umlauts, diacritics, ё).
- **LLM**: a fake client with recorded responses; tests for JSON parsing/validation, fallback on invalid responses, cache and limits. Manual smoke test against the real model — `make llm-smoke`.
- **Import**: adapters on real exports from `testdata/import/` (golden output in `retain/v1`); re-import with added, changed, and removed words — FSRS progress preserved, no duplicates; term parsing rules (`der …`, `… über A`, `sich …`, parentheses, periods).
- **Determinism**: FSRS fuzz disabled or seeded in tests.
- **Integration**: `httptest` + temporary SQLite, scenario "sign up → import export file → reviews → stats".
- **Sync**: time-shuffled logs from two "devices" → final state matches sequential replay.
- **Frontend**: vitest (ts-fsrs on the same golden vectors, palette contrast), Playwright e2e for the main flow, including offline (`context.setOffline`).
- **Lint**: `golangci-lint`, `eslint`, `tsc --noEmit`.

---

## 15. Deployment (home server + Cloudflare Tunnel)

- **Build**: multi-stage Dockerfile — `node` builds `web/dist` → `golang` builds the binary with `CGO_ENABLED=0` → `distroless/static`.
- **docker-compose**:
  - `retain` — listens only on the internal Docker network (`:8080`), **no ports exposed**; volume `/data/retain.db`.
  - `cloudflared` — `tunnel run` with `TUNNEL_TOKEN`, ingress `https://retain.<domain>` → `http://retain:8080`.
  - `litestream` — continuous DB replication to **Cloudflare R2** (must be off-site: disks die).
- **Cloudflare**:
  - TLS terminates at Cloudflare; `Secure` cookies work because the client sees HTTPS.
  - The real client IP comes from `CF-Connecting-IP` (trusted, since the tunnel is the only entry point).
  - Cache Rules: **bypass for `/api/*`**; `sw.js` and `manifest.webmanifest` served with `Cache-Control: no-cache`; hashed assets — `immutable`.
  - Disable Rocket Loader and other JS optimizations (they break SPA/SW).
  - Limits: request body up to 100 MB (imports fit), 100 s response timeout (SSE uses heartbeats).
  - Do **not** enable Cloudflare Access on the app itself — it breaks PWA and push; we have our own invite-based auth.
- **SQLite**: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, a single write connection.
- **Operations**: `/healthz`, logs to stdout, optional `/metrics` (Prometheus). Restore: `litestream restore` — test it manually once.
- **Memory target**: optimize the server Go process for approximately 50 MiB RSS during idle and ordinary operation. This is a goal, not a hard limit; build/browser memory is excluded, and temporary peaks during heavy operations (e.g. large imports) are acceptable.
- **Secrets** (`TUNNEL_TOKEN`, `LLM_API_KEY`, VAPID keys, R2 keys) — in `.env`, never in the repo.
- First admin: `retain admin create --email ...`.

---

## 16. Stages

Each stage is a complete, verifiable chunk; the next one starts once the previous stage's tests are green.

| Stage | Contents |
|---|---|
| **0. Skeleton** | Repo, Makefile, config, SQLite + goose + sqlc, frontend embedding, CI |
| **1. Design system** | Runtime dynamic color from seed (scheme styles, contrast, no-flash cache), tokens (typography, shape, motion), base UI kit components, i18n module, `/dev/ui` with a seed switcher |
| **2. Accounts** | Invites, sign-up, sign-in, sessions, middleware |
| **3. Decks and words** | CRUD, deck tree, card generation, import (preview/apply, retain + school-wordsets + csv adapters), export, deck UI |
| **4. Reviews (core)** | `srs` + golden tests, queue, limits, review, undo, session screen, TTS |
| **5. Modes and languages** | `internal/lang`, typed answers, character bar, multiple choice |
| **6. LLM** | Client, prompts, auto-fill, deck enrichment (jobs), "ask", mnemonics, synonym check, cache and limits |
| **7. Analytics** | Stats endpoints + charts |
| **8. PWA and offline** | Service worker, IndexedDB, ts-fsrs, `/sync`, sync tests |
| **9. Social** | Accepted-friend management, copy/live read-only deck sharing, opt-in aggregate activity and UTC weekly leaderboard |
| **10. Reminders** | Web Push, worker |
| **11. Deployment** | Docker, cloudflared, Litestream → R2, backup restore check |

**MVP = stages 0–4 + 11.** **v1.0 = all stages.**

---

## 17. Open questions

None. To verify in stage 6 (`make llm-smoke`): `cx/gpt-6-luna` on `ai.pestit.pl` responds with `json_schema` output and streams over SSE. The `json_object` fallback stays in the code in case the model changes.
