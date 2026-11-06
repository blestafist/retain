# Retain — технический план

Приложение для изучения иностранных слов с интервальными повторениями (FSRS), аналитикой обучения и LLM-помощником.

- **Формат:** веб-приложение + PWA (устанавливается на телефон, работает офлайн).
- **Аудитория:** я и друзья (до ~50 пользователей).
- **Языковые пары:** польский → немецкий (учим немецкий на базе польского), русский → английский (учим английский на базе русского).
- **Хостинг:** домашний сервер, наружу через Cloudflare Tunnel на своём домене.
- **Дизайн:** Material Design 3 Expressive, адаптивная тема: акцентный цвет выбирает пользователь, по умолчанию болотный зелёный `#3A4527`.

---

## 1. Цели и не-цели

**Цели**
- Быстро добавлять слова (с автозаполнением через LLM) и учить их карточками с умным расписанием повторений.
- Видеть кривую забывания, прогресс и прогноз нагрузки.
- Работать с телефона как с приложением, в том числе без интернета.
- Делиться колодами с друзьями, видеть общий прогресс.

**Не-цели (пока)**
- Платежи, публичная регистрация, масштабирование на тысячи пользователей.
- Нативные мобильные приложения.
- Готовые встроенные колоды (свои колоды импортируются из выгрузок, раздел 3.5).

---

## 2. Стек

| Слой | Технология | Почему |
|---|---|---|
| Язык бэкенда | **Go 1.23+** | Один бинарник, простой деплой |
| HTTP | `net/http` (роутинг из 1.22+) | Минимум зависимостей |
| БД | **SQLite** через `modernc.org/sqlite` (без CGO), WAL-режим | Для десятков пользователей хватает с запасом, ноль администрирования |
| Доступ к БД | **sqlc** | Типобезопасный код из явного SQL |
| Миграции | **goose** (встроены через `embed`) | Применяются при старте |
| Алгоритм повторений | **`github.com/open-spaced-repetition/go-fsrs/v3`** | Официальная Go-реализация FSRS |
| LLM | **`github.com/openai/openai-go`** с `option.WithBaseURL` | OpenAI-совместимый API; по умолчанию свой облачный эндпоинт `https://ai.pestit.pl/v1` |
| Аутентификация | Сессии в cookie (HttpOnly, Secure, SameSite=Lax) + пароли на **argon2id** | Просто и безопасно, без JWT |
| Push-уведомления | Web Push (VAPID), `github.com/SherClockHolmes/webpush-go` | Напоминания о повторениях |
| Логи | `log/slog` (JSON) | Стандартная библиотека |
| Конфиг | env-переменные + `caarlos0/env` | 12-factor |
| Фронтенд | **TypeScript + React + Vite** | Быстрая разработка |
| Данные на клиенте | TanStack Query + **Dexie** (IndexedDB) | Кэш и офлайн-очередь |
| Расписание на клиенте (офлайн) | **`ts-fsrs`** | Та же модель FSRS, что и на сервере |
| Дизайн-система | **MD3 Expressive**: токены из `@material/material-color-utilities` + свои компоненты на **Radix Primitives** + Tailwind | См. раздел 8 |
| Анимации | **Motion** (`motion`), пружинные анимации | Motion scheme MD3 Expressive |
| Шрифт | **Roboto Flex** (variable) | Emphasized-типографика MD3 Expressive |
| Графики | Recharts (или uPlot для тяжёлых) | Кривые, heatmap, прогноз |
| PWA | `vite-plugin-pwa` (Workbox) | Service worker, manifest, кэш |
| Озвучка | Web Speech API (`speechSynthesis`) | Бесплатно, без сервера |
| Деплой | Docker Compose: приложение + **cloudflared** + **Litestream** | Домашний сервер без открытых портов |
| Бэкапы | Litestream → **Cloudflare R2** | Копия вне дома |
| CI | GitHub Actions: `go test`, `golangci-lint`, `vitest`, сборка образа | |

Фронтенд собирается в `web/dist` и встраивается в Go-бинарник через `embed.FS`, поэтому деплоится **один бинарник**.

---

## 3. Функционал

### 3.1 MVP
1. **Аккаунты по инвайтам**: админ создаёт инвайт-код, друг регистрируется по ссылке.
2. **Колоды**: создание, переименование, удаление, языковая пара (`de`/`pl`, `en`/`ru`). Колоды **вложенные**: группа → колоды (например, класс → главы учебника), учить можно одну главу или всю группу сразу.
3. **Слова (notes)**: термин (слово или фраза) и перевод — обязательные; транскрипция, пример, заметка, теги и языковые поля (артикль, мн. число, управление падежом и т.п.) — опциональные. Модель универсальная, см. раздел 3.5.
   - Из одного слова генерируются карточки: `прямая` (термин → перевод) и, по желанию, `обратная` (перевод → термин).
4. **Сессия повторения**:
   - Очередь: просроченные повторения → карточки на изучении → новые (с учётом дневных лимитов). Для родительской колоды — по всем дочерним.
   - Показ карточки → «Показать ответ» → оценки **Again / Hard / Good / Easy**. На каждой кнопке — интервал до следующего показа («10м», «3д», «2мес»).
   - Горячие клавиши (Space, 1–4), свайпы на мобилке, Undo последнего ответа.
5. **Режимы упражнений**:
   - Переворот карточки (классика).
   - Ввод ответа с клавиатуры: нормализация по языку (раздел 3.4) + расстояние Левенштейна → предлагаемая оценка (точно → Good, опечатка → Hard, неверно → Again).
   - Выбор из 4 вариантов (дистракторы из той же колоды) — для новых карточек.
6. **Озвучка** слова через TTS по кнопке и автоматически (настраивается).
7. **Импорт** в универсальном формате `retain/v1` и через адаптеры (школьная выгрузка `classes/wordsets`, CSV): предпросмотр перед применением, повторный импорт без потери прогресса (раздел 3.5). **Экспорт** в `retain/v1` и CSV.
8. **Настройки**: новых в день, максимум повторений в день, желаемое удержание (по умолчанию 0.90), шаги изучения (`1m 10m`), часовой пояс, начало «нового дня» (по умолчанию 04:00), оформление: акцентный цвет, стиль схемы, контраст, светлая/тёмная/системная тема (раздел 8.1).
9. **Базовая статистика**: сегодня повторено/выучено, серия дней (streak), сколько карточек к повторению.

### 3.2 Версия 1.0
- **LLM-помощник** (раздел 6): автозаполнение слова, массовое дополнение колоды, вопросы о слове, мнемоники, проверка синонимов.
- **Полная аналитика** (раздел 5).
- **PWA офлайн**: повторения без сети, синхронизация при появлении сети.
- **Push-напоминания**: «У тебя 34 карточки к повторению» в выбранное время.
- **Общие колоды**: поделиться колодой с другом (копия или подписка на обновления).
- **Друзья**: лента активности и таблица лидеров за неделю (повторения, выученные слова, серия).
- **Пиявки (leeches)**: карточки с 8+ провалами помечаются, LLM предлагает мнемонику и дополнительный пример.
- **Поиск и фильтры** по словам, тегам, состоянию карточки.

### 3.3 Позже
- Адаптер импорта Anki `.apkg` (zip с SQLite внутри).
- Оптимизация параметров FSRS под конкретного пользователя (раздел 4.4).
- Озвучка через OpenAI-совместимый `/audio/speech`, если системных голосов мало.
- Картинки к словам.

### 3.4 Языковые особенности

Карточка: лицевая сторона — слово на изучаемом языке (`de`, `en`), оборот — перевод на родной (`pl`, `ru`). Обратная карточка включается в настройках колоды.

**Нормализация ответа в режиме ввода** (пакет `internal/lang`, интерфейс `Normalizer` на каждый язык):

| Язык | Правила |
|---|---|
| Общие | Unicode NFC, trim, нижний регистр, схлопывание пробелов, удаление пунктуации. Перевод может содержать несколько вариантов через `;` — засчитывается любой. Плюс список `alternatives` (принятые синонимы). |
| `de` | Артикль `der/die/das` у существительных проверяется отдельно: неверный артикль → Hard (настройка колоды: Hard или Again). `ß` = `ss`, `ä/ö/ü` = `ae/oe/ue` (нет немецкой раскладки). Регистр существительных не учитывается. |
| `en` | Игнорируются `to` перед глаголами и артикли `a/an/the`. Британское и американское написание — через `alternatives` (их заполняет LLM). |
| `pl` | Ответ без диакритики (`ą ć ę ł ń ó ś ź ż`) → Hard (почти верно), а не Again. |
| `ru` | `ё` = `е`, знаки ударения (U+0301) удаляются. |

- **Экранная панель символов** в режиме ввода: `ä ö ü ß` для немецкого, `ą ć ę ł ń ó ś ź ż` для польского.
- **TTS-голоса**: `de-DE`, `en-GB`/`en-US`, `pl-PL`, `ru-RU`. Наличие голоса проверяется через `speechSynthesis.getVoices()`; если голоса нет, кнопка скрывается.

### 3.5 Универсальное хранилище и импорт

Внутри всё хранится в одной модели: **коллекция → колоды (вложенные) → заметки → карточки**. Внешние выгрузки в неё приводятся адаптерами или твоим конвертером. Ядро приложения про исходные форматы ничего не знает.

**Модель заметки**
- `term` — термин (слово или фраза), `translation` — перевод. Только эти поля обязательные.
- Опциональные поля: `transcription`, `example`, `example_translation`, `notes`, `tags`, `alternatives` (дополнительные принятые ответы).
- `fields` — произвольный JSON для языковых данных: `kind` (word/phrase), `pos`, `article`, `plural`, `governs` (падеж: A/D/G), `reflexive`, `verb_forms`, `cefr`, `mnemonic` и всё, что понадобится позже. Схема БД при этом не меняется.
- `external_id` — ID в исходной системе (если есть), чтобы повторный импорт обновлял, а не дублировал.
- `term_key` — нормализованный термин (нижний регистр, NFC, без лишних пробелов), используется для поиска дублей.

**Формат обмена `retain/v1`** — целевой формат для конвертеров, он же формат экспорта. JSON Schema отдаётся по `/schema/retain-v1.json`.

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

- Колоды вкладываются друг в друга на любую глубину. `lang_*` наследуется от родителя.
- Обязательны только `format`, `decks[].name`, `notes[].term` и `notes[].translation`. Всё остальное можно не указывать: LLM дополнит пустые поля.

**Адаптеры** (`internal/importer/adapters/`)
```go
type Adapter interface {
    Name() string
    Detect(raw []byte) bool            // автоопределение формата
    Parse(raw []byte) (*Bundle, error) // → retain/v1 в памяти
}
```
- `retain` — родной формат.
- `school-wordsets` — выгрузка вида `classes[].class / wordsets[].info / entries[]`: `class` → родительская колода (`external_id = class:<group_id>`), `wordset` → дочерняя (`wordset:<info.id>`, `expected_count`), `entries` → заметки. Если число слов не совпадает с `expected_count`, выдаётся предупреждение. Языков в файле нет, поэтому пара выбирается на предпросмотре (по умолчанию de/pl).
- `csv` — `term;translation;example;tags`.
- Позже: Anki `.apkg`.

Если конвертировать файлы самому, достаточно выдавать `retain/v1`, адаптер не нужен. `school-wordsets` встроен для удобства, чтобы не гонять конвертер руками.

**Обогащение при импорте** (`internal/lang`, без LLM, детерминированно; результат пишется в `fields`)
- `de`: `der/die/das` + слово с заглавной → `pos=noun`, `article`. Окончание ` A` / ` D` / ` G` → `governs` (`berichten über A`). `sich …` → `reflexive`. 3+ слов без артикля → `kind=phrase`.
- Перевод: части через `,` или `;` → `alternatives`. Пояснения в скобках (`powstanie (np. zbrojne)`) уходят в `notes` и не требуются при вводе. Точка в конце (`Mam propozycję.`) убирается.
- Ввод ответа: маркеры `A/D/G` вводить не нужно, предлог нужен. Артикль проверяется по правилам из раздела 3.4.
- Опечатки в источнике (`donisić`) не исправляются молча. LLM может их подсветить, решение за пользователем.

**Процесс импорта**
1. `POST /import/preview`: файл сохраняется в `import_batches` вместе с исходником (gzip), адаптер определяется автоматически. В ответе дерево колод и по каждой счётчики: новые / изменённые / без изменений / предупреждения.
2. Пользователь выбирает языковую пару и куда положить колоды, потом `POST /import/{batch_id}/apply`. Всё применяется в одной транзакции.
3. **Повторный импорт** (учитель добавил слова в главу): колода ищется по `(owner, source, external_id)`, заметка — по `external_id`, если его нет, то по `term_key`.
   - Новые слова добавляются.
   - У изменённых обновляется перевод, **прогресс FSRS сохраняется**.
   - Исчезнувшие из источника слова по умолчанию остаются. Опция: приостановить их.
4. Исходники остаются в `import_batches`, так что можно переразобрать их улучшенным адаптером без повторной загрузки.
5. После импорта можно запустить «Дополнить через LLM» для пустых полей.

---

## 4. Алгоритм обучения (FSRS)

### 4.1 Модель
FSRS (Free Spaced Repetition Scheduler) — модель памяти DSR. Для каждой карточки хранятся:
- **S (stability)** — стабильность: через сколько дней вероятность вспомнить упадёт до 90%.
- **D (difficulty)** — сложность карточки, 1..10.
- **R (retrievability)** — вероятность вспомнить прямо сейчас; вычисляется, не хранится.

Кривая забывания (FSRS-4.5/5):

```
R(t, S) = (1 + FACTOR * t / S) ^ DECAY,   DECAY = -0.5,  FACTOR = 19/81
```
где `t` — дни с последнего повторения. При `t = S` получается `R = 0.9`.

Следующий интервал вычисляется из желаемого удержания `r_d` (по умолчанию 0.9):
```
I(r_d, S) = S / FACTOR * (r_d ^ (1/DECAY) - 1)
```

### 4.2 Состояния карточки
```
New ──(первый ответ)──▶ Learning ──(прошла шаги)──▶ Review
                             ▲                        │
                             └──── Relearning ◀───(Again)
```
- **Learning/Relearning** — короткие шаги внутри дня (`1m, 10m`).
- **Review** — длинные интервалы по FSRS.
- **Fuzz** интервала (встроен в go-fsrs), чтобы карточки не сбивались в кучу в один день.

### 4.3 Реализация
- Пакет `internal/srs` — тонкая обёртка над `go-fsrs`:
  ```go
  type Scheduler interface {
      Preview(card Card, now time.Time) map[Rating]Card // интервалы для 4 кнопок
      Review(card Card, rating Rating, now time.Time) (Card, ReviewLog)
      Retrievability(card Card, now time.Time) float64
  }
  ```
- Параметры (веса `w[]`, `request_retention`, `maximum_interval`, `enable_fuzz`) хранятся в `user_settings`; по умолчанию стандартные веса FSRS.
- **Сервер — источник истины.** Каждый ответ пишется в `review_logs` (append-only). Состояние карточки можно пересчитать, проиграв логи по порядку — это основа офлайн-синхронизации и undo.
- На клиенте `ts-fsrs` с теми же параметрами — для офлайн-режима и мгновенного показа интервалов.
- **Кросс-тест**: golden-файл с последовательностями ответов; Go- и TS-реализации обязаны выдавать одинаковые S/D/due (допуск 1e-6).

### 4.4 Персональная оптимизация (позже)
- Когда у пользователя 1000+ повторений, по кнопке «Оптимизировать» экспортируем `review_logs` и прогоняем официальный оптимизатор (`fsrs-rs` CLI или Python `fsrs-optimizer`) отдельным процессом/контейнером.
- Новые веса сохраняем с версией, показываем метрики до/после (log loss, RMSE).

---

## 5. Аналитика («кривая обучения»)

Все метрики считаются из `review_logs` и `cards` SQL-запросами; тяжёлые агрегаты кэшируются на 5 минут.

| График / метрика | Что показывает | Источник |
|---|---|---|
| **Кривая забывания карточки** | R(t) от последнего повторения до следующего, точки прошлых ответов | S карточки + её логи |
| **Средняя кривая по колоде** | Как в среднем держатся слова колоды | медиана S по колоде |
| **Реальное vs желаемое удержание** | % правильных ответов на Review-карточках по неделям против 90% | `review_logs` |
| **Прогноз нагрузки** | Сколько карточек будет к повторению каждый день ближайшие 30 дней | `cards.due` |
| **Heatmap активности** | Повторения по дням за год | `review_logs` |
| **Распределение состояний** | New / Learning / Review / Relearning; «молодые» (S < 21д) и «зрелые» (S ≥ 21д) | `cards` |
| **Рост словаря** | Сколько слов «выучено» (S ≥ 21д) во времени, отдельно по языкам | `review_logs` |
| **Распределение S и D** | Гистограммы стабильности и сложности | `cards` |
| **Время ответа** | Среднее время на карточку, общее время в день | `review_logs.duration_ms` |
| **Пиявки** | Топ проблемных слов по числу провалов | `cards.lapses` |
| **«Сколько слов знаю сейчас»** | Σ R(now) по всем Review-карточкам | вычисление в Go |

---

## 6. LLM-помощник (OpenAI-совместимый API)

### 6.1 Подключение
- Пакет `internal/llm`, клиент `openai-go` с настраиваемым `base_url`:
  ```
  LLM_BASE_URL=https://ai.pestit.pl/v1        # облачный OpenAI-совместимый эндпоинт
  LLM_API_KEY=...                             # Bearer-токен (без него эндпоинт отвечает 401)
  LLM_MODEL=cx/gpt-6-luna                     # модель по умолчанию
  LLM_MODEL_FAST=                             # опционально: другая модель для массового дополнения колод (по умолчанию = LLM_MODEL)
  LLM_TIMEOUT=30s
  LLM_DAILY_LIMIT_PER_USER=300                # запросов в день
  ```
- Ключ хранится **только на сервере**; клиент ходит в LLM через наш API.
- Если `LLM_BASE_URL` не задан, все LLM-функции скрыты в UI, приложение работает без них.
- При старте сервер запрашивает `GET /v1/models` и проверяет, что `LLM_MODEL` есть в списке; если нет — предупреждение в логах и в админке. В админке модель можно сменить без рестарта (список берётся из `/v1/models`, выбор хранится в `app_settings`).
- Запросы к эндпоинту исходящие, Cloudflare Tunnel им не мешает. Ретраи с backoff на 429/5xx, circuit breaker: при недоступности LLM-кнопки временно неактивны, остальное приложение работает.

### 6.2 Функции
| Функция | Когда | Что возвращает |
|---|---|---|
| **Автозаполнение слова** | При добавлении слова — кнопка «Заполнить» или автоматически после ввода | Варианты перевода, транскрипция (IPA), часть речи, для `de`-существительных артикль и мн. число, для глаголов основные формы, 1–2 примера с переводом, уровень CEFR, `alternatives` |
| **Дополнение колоды** | После импорта или по кнопке | Фоновая задача: пустые поля заполняются пачками по 20 слов, прогресс в UI |
| **Спросить про слово** | Кнопка на карточке/в слове | Ответ в чате со стримингом (SSE): разница синонимов, употребление, грамматика |
| **Мнемоника** | Для пиявок | 2–3 варианта ассоциаций на родном языке пользователя |
| **Проверка синонима** | В режиме ввода, когда ответ не совпал — кнопка «Мой ответ тоже верный?» | Вердикт + объяснение; при «да» ответ добавляется в `alternatives`, оценку можно исправить |

### 6.3 Правила
- **LLM ничего не меняет без подтверждения**: результат показывается как предложение, пользователь принимает его целиком или по полям.
- **Структурированный вывод**: `response_format: json_schema`. Если провайдер его не поддерживает — fallback на `json_object`, валидация в Go и один повтор при невалидном JSON.
- **Промпты** лежат в `internal/llm/prompts/*.tmpl` (embed), учитывают языковую пару; для слов с несколькими значениями возвращается список.
- **Кэш**: ответы автозаполнения кэшируются в `llm_cache` по `sha256(model + prompt_version + lang_pair + word)` — одно и то же слово у разных друзей не оплачивается дважды.
- **Лимиты**: дневной лимит на пользователя (`llm_usage`), учёт токенов для статистики расходов в админке.
- **SSE через Cloudflare**: `Content-Type: text/event-stream`, heartbeat-комментарий каждые 15 с, чтобы не упереться в таймаут 100 с.

---

## 7. Модель данных (SQLite)

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

app_settings(key TEXT PK, value TEXT /* JSON */, updated_at)   -- например, llm_model

decks(
  id INTEGER PK, owner_id → users, parent_id → decks NULL, -- вложенность
  name, description, position INT,
  lang_target TEXT,                  -- 'de' | 'en' (NULL → от родителя)
  lang_native TEXT,                  -- 'pl' | 'ru'
  reverse_cards BOOL DEFAULT 0,
  wrong_article_rating INT DEFAULT 2, -- Hard
  color_seed TEXT NULL,              -- свой акцент колоды
  source TEXT NULL, external_id TEXT NULL, expected_count INT NULL,
  source_deck_id NULL,               -- если скопирована у друга
  created_at, updated_at, deleted_at NULL)
  UNIQUE(owner_id, source, external_id), INDEX(parent_id)

deck_shares(deck_id → decks, user_id → users, mode TEXT /* copy|subscribe */)

notes(                               -- «слово или фраза»
  id INTEGER PK, deck_id → decks,
  term TEXT NOT NULL, term_key TEXT NOT NULL, -- term_key: нормализованный, для дублей
  translation TEXT NOT NULL, transcription, example, example_translation, notes,
  tags TEXT /* JSON */, alternatives TEXT /* JSON */,
  fields TEXT /* JSON: kind, pos, article, plural, governs, reflexive, verb_forms, cefr, mnemonic, ... */,
  external_id TEXT NULL, import_batch_id → import_batches NULL,
  created_at, updated_at, deleted_at NULL)
  UNIQUE(deck_id, term_key), UNIQUE(deck_id, external_id)

import_batches(
  id TEXT PK, user_id → users, adapter TEXT, filename TEXT,
  raw BLOB /* gzip исходника */, raw_sha256 BLOB,
  status TEXT /* previewed|applied|failed */, summary TEXT /* JSON: счётчики, предупреждения */,
  created_at, applied_at NULL)

cards(
  id INTEGER PK, note_id → notes, user_id → users,
  direction TEXT /* forward|reverse */,
  state INT /* 0 New,1 Learning,2 Review,3 Relearning */,
  due DATETIME, stability REAL, difficulty REAL,
  elapsed_days INT, scheduled_days INT, step INT,
  reps INT, lapses INT, last_review DATETIME NULL,
  suspended BOOL, is_leech BOOL, updated_at)
  INDEX(user_id, due), INDEX(note_id)

review_logs(                         -- append-only
  id TEXT PK /* UUIDv7 от клиента — идемпотентность */,
  card_id → cards, user_id → users,
  rating INT /* 1..4 */, mode TEXT /* flip|typing|choice */,
  state_before INT, stability_before REAL, difficulty_before REAL,
  stability_after REAL, difficulty_after REAL, scheduled_days INT,
  reviewed_at DATETIME /* время клиента */, received_at DATETIME,
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

Карточки привязаны к `user_id`: при подписке на колоду друга заметки общие, а прогресс у каждого свой.

Фоновые задачи (`jobs`) выполняет простой воркер-пул внутри процесса; при рестарте задачи в статусе `running` возвращаются в очередь.

---

## 8. Дизайн: Material Design 3 Expressive

### 8.1 Цвет: адаптивная тема
Вся палитра строится в рантайме из одного **seed-цвета** (MD3 dynamic color). Пользователь меняет акцент — меняется всё приложение.

- **По умолчанию**: болотный зелёный **`#3A4527`**.
- **Настройки → Оформление**:
  - Пресеты-«пятна» (MD3 color swatches): болотный `#3A4527` (по умолчанию), плюс 6–8 заранее подобранных оттенков.
  - Свой цвет: color picker + поле HEX.
  - **Стиль схемы**: `Fidelity` (по умолчанию — точнее всего держит заданный оттенок), `TonalSpot` (спокойнее), `Vibrant`, `Expressive` (сильнее вращает тон, ярче), `Monochrome`.
  - **Контраст**: стандартный / средний / высокий (`contrastLevel` 0 / 0.5 / 1).
  - Тема: светлая / тёмная / системная.
  - Предпросмотр вживую (карточка, кнопки оценок, график) до сохранения.
- **Цвет колоды** (опционально): у колоды свой seed; во время сессии по этой колоде тема перекрашивается в её цвет. Помогает различать немецкий и английский с одного взгляда.
- **Генерация**: `@material/material-color-utilities` (`Hct`, `SchemeFidelity`/`SchemeTonalSpot`/..., `DynamicScheme`, `MaterialDynamicColors`) прямо в браузере → CSS-переменные `--md-sys-color-*` (primary, on-primary, primary-container, secondary, tertiary, surface, surface-container-lowest…highest, outline, error и т.д.) на `:root`. Пересчёт занимает < 5 мс.
- **Без мигания при загрузке**: последняя рассчитанная палитра кэшируется в `localStorage`; inline-скрипт в `index.html` применяет её до отрисовки React. Без кэша — встроенная палитра по умолчанию, сгенерированная на этапе сборки (`web/scripts/gen-theme.ts`).
- **Синхронизация**: `theme_seed`, `theme_variant`, `theme_contrast`, `theme_mode` хранятся в `user_settings` и подтягиваются на всех устройствах.
- Tailwind-тема ссылается только на переменные (`bg-primary-container`, `text-on-surface` и т.д.); **жёстко заданных цветов в компонентах нет** (правило линтера).
- **Семантические цвета не зависят от seed**: кнопки оценок берут гармонизированные (`Blend.harmonize`) фиксированные оттенки, чтобы Again всегда оставался «красным», а Good — «зелёным» при любом акценте: Again → error, Hard → оранжевый, Good → зелёный, Easy → синий, каждый в своём `container`-варианте.
- **Графики**: серии берут цвета из `primary` / `tertiary` / `secondary` и семантических ролей.
- `theme-color` в `<meta>` обновляется вместе с палитрой (цвет статус-бара на Android).
- **Доступность**: при выборе своего цвета контраст пар `on-*`/`*` проверяется по WCAG AA; если не проходит — предупреждение и предложение поднять контраст. Тесты прогоняют все пресеты × стили × светлая/тёмная.

### 8.2 Типографика
- **Roboto Flex** (variable, self-hosted через `@fontsource-variable`) с поддержкой латиницы с диакритикой и кириллицы.
- Шкала MD3 (display / headline / title / body / label) + **emphasized**-стили Expressive для ключевых мест: слово на карточке — `display-large-emphasized`, итог сессии и серия — `headline-emphasized`.

### 8.3 Форма
- Шкала скруглений MD3 с расширенными значениями Expressive (`extra-large-increased`, `extra-extra-large` и т.п.) в виде CSS-токенов `--md-sys-shape-corner-*`.
- **Shape morphing**: кнопки при нажатии меняют скругление (round → square), выбранные элементы — форму.
- Абстрактные формы Expressive (cookie, clover, sunny, ...) — SVG для аватаров друзей, бейджа серии и индикатора загрузки.

### 8.4 Движение
- Пружинный **motion scheme** Expressive: spatial-пружины (положение, размер, форма) и effects-пружины (цвет, прозрачность), каждая в вариантах fast / default / slow. Параметры — токены в `web/src/theme/motion.ts`, реализация через Motion.
- Переворот карточки, вылет карточки после ответа, появление следующей — пружинные анимации.
- `prefers-reduced-motion` → анимации заменяются на кроссфейд.
- Тактильный отклик `navigator.vibrate` на Android при оценке.

### 8.5 Компоненты и раскладка
| Место | Компонент MD3 Expressive |
|---|---|
| 4 кнопки оценки | **Connected button group** |
| Добавить слово / импорт / колода | **FAB menu** |
| Прогресс сессии | **Wavy linear progress indicator** |
| Загрузка, ожидание LLM | **Loading indicator** (морфинг форм) |
| Действия в сессии (undo, TTS, редактировать, спросить LLM) | **Floating toolbar** |
| «Учить» + выбор режима на колоде | **Split button** |
| Навигация | **Navigation bar** (compact) / **navigation rail** (≥ 840px) |
| Undo, ошибки | Snackbar |
| Предложения LLM | Bottom sheet (mobile) / side sheet (desktop) с принятием по полям |

- Window size classes: compact < 600, medium 600–839, expanded ≥ 840.
- Touch targets не меньше 48dp, видимый focus ring, контраст по WCAG AA (проверка сгенерированной палитры в тестах).

### 8.6 Реализация
- Готовой официальной веб-библиотеки компонентов MD3 Expressive на момент планирования нет (`@material/web` в режиме поддержки и без Expressive). Поэтому свой небольшой UI-кит в `web/src/ui/`:
  поведение и доступность — **Radix Primitives**, внешний вид — Tailwind + MD3-токены, анимации — Motion.
- Страница `/dev/ui` (только в dev) — витрина всех компонентов в обеих темах.

---

## 9. API (REST, JSON, `/api/v1`)

```
POST   /auth/register            {invite, email, name, password}
POST   /auth/login               {email, password}       → Set-Cookie
POST   /auth/logout
GET    /me
PATCH  /me/settings

POST   /admin/invites            → {code, url}           (только админ)
GET    /admin/llm-usage          расход токенов по пользователям
GET    /admin/llm/models         список моделей с эндпоинта (/v1/models)
PUT    /admin/llm/model          {model} — сменить модель без рестарта

GET    /decks                    дерево колод + счётчики (new/learning/due, с учётом дочерних)
POST   /decks
PATCH  /decks/{id}
DELETE /decks/{id}
POST   /import/preview           multipart: файл (+ adapter, если не определился) → {batch_id, tree, counts, warnings}
POST   /import/{batch_id}/apply  {lang_target, lang_native, parent_deck_id?, on_missing: keep|suspend}
GET    /import/batches           история импортов
GET    /schema/retain-v1.json    JSON Schema формата обмена
GET    /decks/{id}/export?format=retain|csv   (с дочерними колодами)
POST   /decks/{id}/share         {user_id, mode}
POST   /decks/{id}/enrich        → {job_id}  дополнить пустые поля через LLM

GET    /decks/{id}/notes?q=&tag=&state=&cursor=
POST   /decks/{id}/notes         создаёт note + карточки
PATCH  /notes/{id}
DELETE /notes/{id}

POST   /llm/enrich               {deck_id, word}         → предложения полей
POST   /llm/ask                  {note_id, question}     → SSE-поток
POST   /llm/mnemonic             {note_id}
POST   /llm/check-answer         {card_id, answer}       → {accepted, explanation}

GET    /jobs/{id}                статус и прогресс фоновой задачи

GET    /study/queue?deck_id=&limit=50   карточки + preview интервалов
POST   /study/review             {id(uuid), card_id, rating, mode, reviewed_at, duration_ms}
POST   /study/undo               {review_id}
POST   /cards/{id}/suspend | /unsuspend | /reset

POST   /sync                     {reviews:[...], since} → {applied, changed_cards, changed_notes}

GET    /stats/overview           streak, сегодня, всего
GET    /stats/forecast?days=30
GET    /stats/retention?period=week
GET    /stats/heatmap?year=
GET    /stats/cards/{id}/curve
GET    /stats/distribution

GET    /friends/activity
GET    /friends/leaderboard?period=week

GET    /push/vapid-key
POST   /push/subscribe | DELETE /push/subscribe
```

Ошибки: `{"error": {"code": "not_found", "message": "..."}}`. Защита от CSRF: SameSite cookie + проверка заголовка `Origin`.

---

## 10. Офлайн и синхронизация

1. Service worker кэширует оболочку приложения (precache) и GET-запросы API (stale-while-revalidate).
2. При старте сессии клиент забирает очередь (`/study/queue`) с запасом на день и кладёт её в IndexedDB.
3. Офлайн-ответ: `ts-fsrs` считает новое состояние локально → карточка обновляется в IndexedDB → лог с UUIDv7 и `reviewed_at` кладётся в очередь `outbox`.
4. При появлении сети: `POST /sync` с пачкой логов.
   - Сервер вставляет логи идемпотентно (`ON CONFLICT(id) DO NOTHING`).
   - Если для карточки пришли логи «из прошлого» (с другого устройства), сервер **пересчитывает карточку, проигрывая все её логи по `reviewed_at`**.
   - В ответе — актуальные карточки; клиент перезаписывает свой кэш.
5. Конфликты редактирования слов: last-write-wins по `updated_at`.
6. LLM-функции офлайн недоступны — кнопки неактивны с подсказкой.

---

## 11. Напоминания

- Фоновая горутина (`time.Ticker` раз в минуту) находит пользователей, у которых в их часовом поясе наступило `reminder_time` и есть карточки к повторению → Web Push.
- Не больше одного пуша в день; подписки, на которые push-сервис отвечает 410 Gone, удаляем.
- Push — исходящие запросы, Cloudflare Tunnel им не мешает.
- На iOS Web Push работает только для PWA, установленного на главный экран (iOS 16.4+) — пишем об этом в UI.

---

## 12. Структура проекта

```
retain/
├── cmd/retain/main.go            # старт: конфиг, БД, миграции, HTTP, воркеры
├── internal/
│   ├── config/
│   ├── db/
│   │   ├── migrations/*.sql      # goose
│   │   ├── queries/*.sql         # sqlc
│   │   └── gen/                  # сгенерированный код
│   ├── auth/                     # argon2id, сессии, middleware
│   ├── srs/                      # обёртка go-fsrs, пересчёт карточки по логам
│   ├── lang/                     # нормализаторы de/en/pl/ru, сравнение ответов, разбор терминов
│   ├── study/                    # очередь, лимиты, review, undo
│   ├── decks/  notes/            # CRUD, дерево колод, экспорт
│   ├── importer/                 # preview/apply, upsert по external_id/term_key
│   │   └── adapters/             # retain, school-wordsets, csv, (apkg)
│   ├── llm/                      # клиент, промпты (embed), кэш, лимиты, SSE
│   ├── jobs/                     # фоновые задачи (дополнение колоды)
│   ├── stats/                    # аналитика
│   ├── sync/                     # офлайн-синхронизация
│   ├── social/                   # шаринг, лидерборд, активность
│   ├── push/                     # VAPID, воркер напоминаний
│   └── httpapi/                  # роуты, хендлеры, ошибки, middleware
├── web/                          # React + Vite + TS
│   ├── scripts/gen-theme.ts      # палитра по умолчанию на этапе сборки
│   ├── src/
│   │   ├── theme/                # dynamic color из seed, токены: типографика, форма, motion
│   │   ├── ui/                   # UI-кит MD3 Expressive (Radix + Tailwind + Motion)
│   │   ├── pages/  components/
│   │   ├── api/  db/  srs/  sw/
│   └── dist/                     # встраивается через embed
├── testdata/
│   ├── fsrs_golden.json          # общие тест-векторы для Go и TS
│   ├── lang_cases.json           # кейсы нормализации ответов
│   └── import/                   # примеры выгрузок для тестов адаптеров
├── sqlc.yaml
├── Dockerfile
├── docker-compose.yml            # retain + cloudflared + litestream
├── Makefile                      # dev, gen, test, lint, build
└── PLAN.md
```

Хендлеры тонкие → сервисы (`study`, `stats`, `llm`, ...) → репозитории на sqlc. Сервисы принимают `clock.Clock` (подмена времени в тестах), `llm.Client` — интерфейс (фейк в тестах).

---

## 13. Экраны фронтенда

1. **Вход / регистрация по инвайту**.
2. **Главная**: колоды со счётчиками (новые / на изучении / к повторению), split button «Учить», серия дней, FAB menu.
3. **Сессия**: карточка, wavy-прогресс, connected button group с интервалами, floating toolbar (undo, TTS, редактировать, спросить LLM), итог сессии в конце.
4. **Колода**: список слов, поиск, фильтры, быстрое добавление (Enter → автозаполнение LLM → подтверждение → следующее слово), импорт/экспорт, «Дополнить через LLM».
5. **Слово**: поля, история ответов, кривая забывания, S/D/R, чат «Спросить про слово».
6. **Статистика**: графики из раздела 5.
7. **Друзья**: активность, лидерборд, общие колоды.
8. **Настройки**: лимиты, удержание, шаги, напоминания, часовой пояс; **Оформление** — пресеты цвета, color picker, стиль схемы, контраст, тема, живой предпросмотр.

Mobile-first; свайп влево = Again, вправо = Good.

---

## 14. Тестирование

- **Unit (Go)**: `srs` — golden-векторы и граничные случаи (первый ответ, Again на Review, максимальный интервал); `study` — очередь и дневные лимиты с учётом часового пояса и `day_start_hour`; `lang` — таблица кейсов из `lang_cases.json` (артикли, умлауты, диакритика, ё).
- **LLM**: фейковый клиент с записанными ответами; тесты парсинга/валидации JSON, fallback при невалидном ответе, кэш и лимиты. Ручной smoke-тест на реальной модели — `make llm-smoke`.
- **Импорт**: адаптеры на реальных выгрузках из `testdata/import/` (golden-результат в `retain/v1`); повторный импорт с добавленными, изменёнными и удалёнными словами — прогресс FSRS сохраняется, дублей нет; правила разбора терминов (`der …`, `… über A`, `sich …`, скобки, точки).
- **Детерминизм**: в тестах fuzz FSRS отключён или задан seed.
- **Интеграционные**: `httptest` + временная SQLite, сценарий «регистрация → импорт выгрузки → повторения → статистика».
- **Sync**: перемешанные по времени логи с двух «устройств» → итоговое состояние совпадает с последовательным проигрыванием.
- **Фронтенд**: vitest (ts-fsrs на тех же golden-векторах, контраст палитры), Playwright e2e на основной сценарий, включая офлайн (`context.setOffline`).
- **Lint**: `golangci-lint`, `eslint`, `tsc --noEmit`.

---

## 15. Деплой (домашний сервер + Cloudflare Tunnel)

- **Сборка**: multi-stage Dockerfile — `node` собирает `web/dist` → `golang` собирает бинарник с `CGO_ENABLED=0` → `distroless/static`.
- **docker-compose**:
  - `retain` — слушает только внутреннюю docker-сеть (`:8080`), **порты наружу не открываются**; том `/data/retain.db`.
  - `cloudflared` — `tunnel run` с `TUNNEL_TOKEN`, ingress `https://retain.<домен>` → `http://retain:8080`.
  - `litestream` — непрерывная репликация БД в **Cloudflare R2** (обязательно вне дома: диск может умереть).
- **Cloudflare**:
  - TLS терминируется на Cloudflare; cookie `Secure` работает, т.к. клиент видит HTTPS.
  - Реальный IP клиента берём из `CF-Connecting-IP` (доверяем, т.к. туннель — единственный вход).
  - Cache Rules: **bypass для `/api/*`**; `sw.js` и `manifest.webmanifest` отдаём с `Cache-Control: no-cache`; хэшированные ассеты — `immutable`.
  - Rocket Loader и прочие оптимизации JS выключить (ломают SPA/SW).
  - Лимиты: тело запроса до 100 МБ (импорт влезает), таймаут ответа 100 с (для SSE — heartbeat).
  - Cloudflare Access на само приложение **не включаем** — мешает PWA и push; своя авторизация по инвайтам.
- **SQLite**: `journal_mode=WAL`, `busy_timeout=5000`, `foreign_keys=ON`, одно соединение на запись.
- **Эксплуатация**: `/healthz`, логи в stdout, опционально `/metrics` (Prometheus). Восстановление: `litestream restore` — проверить один раз руками.
- **Секреты** (`TUNNEL_TOKEN`, `LLM_API_KEY`, VAPID-ключи, ключи R2) — в `.env`, не в репозитории.
- Первый админ: `retain admin create --email ...`.

---

## 16. Этапы

Каждый этап — законченный, проверяемый кусок; следующий начинается, когда тесты предыдущего зелёные.

| Этап | Содержимое |
|---|---|
| **0. Каркас** | Репозиторий, Makefile, конфиг, SQLite + goose + sqlc, embed фронта, CI |
| **1. Дизайн-система** | Dynamic color из seed в рантайме (стили схем, контраст, кэш без мигания), токены (типографика, форма, motion), базовые компоненты UI-кита, `/dev/ui` с переключателем seed |
| **2. Аккаунты** | Инвайты, регистрация, логин, сессии, middleware |
| **3. Колоды и слова** | CRUD, дерево колод, генерация карточек, импорт (preview/apply, адаптеры retain + school-wordsets + csv), экспорт, UI колод |
| **4. Повторения (ядро)** | `srs` + golden-тесты, очередь, лимиты, review, undo, экран сессии, TTS |
| **5. Режимы и языки** | `internal/lang`, ввод с клавиатуры, панель символов, выбор из вариантов |
| **6. LLM** | Клиент, промпты, автозаполнение, дополнение колоды (jobs), «спросить», мнемоники, проверка синонимов, кэш и лимиты |
| **7. Аналитика** | Эндпоинты статистики + графики |
| **8. PWA и офлайн** | Service worker, IndexedDB, ts-fsrs, `/sync`, тесты синхронизации |
| **9. Социальное** | Шаринг колод, активность, лидерборд |
| **10. Напоминания** | Web Push, воркер |
| **11. Деплой** | Docker, cloudflared, Litestream → R2, проверка восстановления из бэкапа |

**MVP = этапы 0–4 + 11.** **v1.0 = все этапы.**

---

## 17. Открытые вопросы

Открытых вопросов нет. Проверить на этапе 6 (`make llm-smoke`): `cx/gpt-6-luna` на `ai.pestit.pl` отвечает по `json_schema` и стримит через SSE. Fallback на `json_object` остаётся в коде на случай смены модели.
