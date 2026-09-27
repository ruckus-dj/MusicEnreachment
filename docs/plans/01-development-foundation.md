# План: первый этап разработки — технический фундамент

## Статус документа

Черновик плана. Этап содержит только утверждённые технические решения из
`docs/design/`. Он не утверждает новые продуктовые сценарии, физическую
предметную схему или API-контракты.

## Цель

Создать воспроизводимую основу проекта, на которой можно безопасно реализовывать
последующие отдельно согласованные продуктовые задачи: монорепозиторий, единый
runtime, проверяемые сборки, миграции, API-generation pipeline и frontend shell.

## Сценарии

1. Разработчик клонирует репозиторий и одной документаированной командой
   запускает приложение локально с PostgreSQL.
2. Разработчик собирает frontend и backend; единый Go-бинарник отдаёт
   встроенные static assets.
3. При старте приложение подключается к PostgreSQL, применяет embedded
   миграции, затем запускает River и HTTP server.
4. Разработчик запускает задачу Task для pipeline генерации OpenAPI и Orval; CI
   обнаруживает неактуальный generated-код.
5. Локальные проверки и GitHub CI запускают одинаковый набор форматтеров,
   линтеров, тестов и сборок.

## Границы и ограничения

- Стек: Go 1.27, PostgreSQL, Bun, River, Huma + chi, TypeScript, React Aria,
  Tailwind CSS, Vite и Orval.
- Runtime — один Go-процесс: миграции, River workers, HTTP API и встроенный
  frontend. Порядок запуска: миграции → River → HTTP server.
- Runtime-настройки хранятся в PostgreSQL и изменяются через UI. Переменные
  окружения допускаются только для подключения к PostgreSQL.
- Поддерживаемые платформы: Linux, Windows и macOS в вариантах `amd64` и
  `arm64`. Для автоматической установки инструментов требуются утверждённые
  источники готовых бинарников `ffmpeg`, `ffprobe` и `fpcalc`.
- Исходные файлы read-only по умолчанию; этот этап не добавляет обработку или
  изменение файловой системы источников.
- `docs/plans/` добавляется как каталог планов; он дополняет существующие
  `docs/design/` и `docs/app-design/`.

## Изменения модели данных

В этапе не создаются предметные таблицы медиатеки, matching или публикаций:
`docs/design/data-model.md` не утверждает физическую SQL-схему.

Создаётся только инфраструктура `bun/migrate`: embedded versioned SQL-миграции,
их применение и откат. Любая первая техническая миграция не должна задавать
модель предметных данных.

## Операции и API

В этапе не утверждаются продуктовые REST operations, DTO или read-models.

Создаётся code-first pipeline: Huma экспортирует OpenAPI, а единая задача Task
запускает Orval и обновляет TypeScript-клиент, React Query hooks и
mocks в `frontend/src/api/generated/`. Конкретные endpoints появятся только в
отдельных продуктовых задачах.

## Затронутые компоненты

- `backend/cmd/server`: composition root без предметной логики.
- `backend/internal/app`: startup, lifecycle и wiring.
- `backend/internal/migrations`: `go:embed` и `bun/migrate`.
- `backend/internal/static`: встраивание production-сборки Vite.
- `backend/internal/jobs`: запуск River без предметных job types.
- `frontend/src/api/{generated,client}`, `components`, `routes`, `styles`:
  frontend shell и generated API boundary.
- `deploy/compose`, `deploy/docker`: локальный и Docker запуск.
- `Taskfile.yml`, `tools/`, `.github/workflows/`: единые проверки и генерация.

## Этапы выполнения

### 1. Каркас репозитория и toolchain

Создать утверждённую структуру monorepo: `backend/`, `frontend/`, `deploy/`,
`tools/`, `.github/`, `Taskfile.yml`. Инициализировать Go-модуль и Vite/TypeScript frontend,
подключить React Aria и Tailwind.

**Результат:** backend и frontend собираются в чистом checkout без предметной
логики.

### 2. Единый runtime и локальный запуск

Подключить PostgreSQL, собрать startup lifecycle и отдачу встроенных Vite assets.
Добавить Dockerfile и Docker Compose с PostgreSQL; сохранить возможность запуска
с внешней PostgreSQL вне Docker.

**Результат:** приложение запускается локально и через Compose, подключается к
PostgreSQL и отдаёт frontend из одного Go-процесса.

### 3. Миграции и River bootstrap

Добавить `bun/migrate` с SQL-парами `.tx.up.sql` / `.tx.down.sql`, `go:embed`,
`Migrations.Discover` и применением с `WithMarkAppliedOnSuccess(true)`. Подключить
River и запускать workers только после успешных миграций.

**Результат:** техническая embedded-миграция применима и откатываема без внешних
SQL-файлов в runtime; River не стартует при неуспешной миграции.

### 4. API-generation pipeline и frontend shell

Добавить экспорт OpenAPI из Huma и единую задачу Task для генерации Orval. Подготовить
frontend-маршрутизацию, глобальные стили и базовые React Aria-компоненты без
предметных экранов и вымышленных API-контрактов.

**Результат:** генерация воспроизводима, generated-файлы проверяются CI, а
frontend отображается из Go-бинарника.

### 5. Quality gates

Настроить `gofmt`, `golangci-lint`, Go unit tests, Biome, Vitest и React Testing
Library. Добавить GitHub CI и pre-commit с идентичным набором команд.

**Результат:** локальные проверки и CI дают одинаковый результат; build, test,
lint и format обязательны для обеих частей проекта.

### 6. Граница Setup Manager и внешних инструментов

Подготовить технические интерфейсы и persistent tools-directory для Setup Manager;
проверка установленной версии выполняется через `--version`. Реальная загрузка,
обновление и rollback инструментов начинаются только после фиксации источников
готовых бинарников для Linux/Windows/macOS `amd64`/`arm64`.

**Результат:** следующий этап может реализовать lifecycle инструментов без
изменения архитектурных границ; в первом этапе не используются произвольные URL
и не добавляется загрузка без утверждённого источника.

## Критерии готовности

- Структура проекта соответствует `docs/design/repository-architecture.md`.
- Чистый checkout собирает backend и frontend.
- Локальный запуск и Compose smoke-run успешно подключаются к PostgreSQL.
- Миграции embedded в Go-бинарник, применяются до River и не отмечаются
  применёнными при ошибке.
- Go-процесс отдаёт production-сборку frontend.
- OpenAPI → Orval generation запускается одной командой и проверяется CI.
- CI и pre-commit используют один и тот же набор проверок.
- Реализация не добавляет предметную SQL-схему, продуктовые endpoints или
  неподтверждённые источники бинарников.

## План проверки

1. Выполнить локальные команды build, test, lint, format и generate.
2. Запустить приложение с PostgreSQL через Compose и с внешней PostgreSQL.
3. Убедиться по логам и smoke-check, что миграции завершились до запуска River и
   HTTP server.
4. Убедиться, что приложение отдаёт встроенный frontend и frontend не зависит от
   несгенерированного контракта.
5. Внести намеренное изменение Huma-контракта и подтвердить, что generation/CI
   обнаруживают устаревшие файлы Orval.
6. Запустить успешную и ошибочную технические миграции и проверить корректную
   отметку применения.

## Вне области этапа

- Предметная SQL-схема и миграции медиатеки.
- Сканирование, `ffprobe`, `fpcalc`, fingerprinting и группировка файлов.
- MusicBrainz, LRCLIB, AcousticID и кэширование провайдеров.
- Matching, score, evidence, drafts и пользовательские workflows.
- Публикация файлов, remux, artwork, lyrics и правила именования.
- Реальные Setup-поля, автоматическая загрузка инструментов, обновление и
  rollback до получения утверждённой таблицы источников бинарников.
- Spotify, Apple Music, Deezer, Discogs, object storage, multi-replica
  deployment, обязательный Nginx, Testcontainers и Playwright E2E.
