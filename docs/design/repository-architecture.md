# Архитектура репозитория

## Зафиксированное

- Все компоненты проекта находятся в одном GitHub monorepo.
- Runtime развёртывается прежде всего на NAS или другом локальном сервере и
  работает с его файловой системой. Desktop-first относится к web-интерфейсу в
  браузере, а не к месту выполнения приложения или формату desktop executable.
- В репозиторий войдут единое Go-приложение, TypeScript frontend на Vite,
  документация, Docker Compose и конфигурация единого набора локальных и
  CI-проверок.
- Go-приложение отдаёт Vite static assets, HTTP API и River workers работают в
  одном процессе. Необязательный reverse proxy конкретного deployment отвечает
  за TLS, authentication и внешнюю сетевую границу; backend не реализует
  пользователей, tokens, permissions и обработку proxy identity headers.
- Главный синхронный контракт между frontend и backend — REST API. Huma
  генерирует OpenAPI из Go-кода, и этот OpenAPI является источником истины.
  TypeScript-типы frontend генерируются из этой спецификации.
  Асинхронные механизмы добавляются только когда это требуется продуктовой
  задачей.
- Runtime-настройки хранятся в PostgreSQL и изменяются через UI/Setup Manager.
  Env-ввод ограничен bootstrap-параметрами `DATABASE_URL`,
  `HTTP_BIND_ADDRESS` и `HTTP_PORT`; provider credentials, log level и domain
  settings через env не задаются.
- Базовый Compose запускает PostgreSQL, но поддерживается подключение к внешней
  БД.
- Bun и River разделяют основной `database/sql` pool. Транзакционный enqueue
  выполняется передачей `bun.Tx.Tx` в `River.InsertTx`; выделенный `pgxpool` с
  одним соединением используется только для River `LISTEN/NOTIFY`.

## Утверждённая структура

```text
.
├── backend/
│   ├── cmd/
│   │   └── server/                 # Composition root единого Go-процесса
│   ├── internal/
│   │   ├── app/                    # Startup, lifecycle и wiring компонентов
│   │   ├── api/                    # Huma operations, HTTP middleware и API DTO
│   │   ├── service/                # Application services и оркестрация операций
│   │   ├── persistence/            # Bun models, repositories и транзакции PostgreSQL
│   │   ├── jobs/                   # River job arguments, workers и scheduling
│   │   ├── integrations/           # MusicBrainz, LRCLIB, external tools и будущие providers
│   │   ├── settings/               # Настройки из БД и Setup Manager
│   │   ├── publication/            # Управляемая файловая публикация и staging
│   │   ├── static/                 # Встроенная Vite-сборка, отдаваемая Go-приложением
│   │   └── migrations/             # migrations.go и embedded versioned SQL для bun/migrate
│   ├── go.mod
│   └── go.sum
├── frontend/
│   ├── src/
│   │   ├── api/
│   │   │   ├── generated/          # Сгенерированные из OpenAPI TypeScript-типы
│   │   │   └── client/             # Конфигурация и application-обёртки над Orval SDK
│   │   ├── components/             # Переиспользуемые React Aria компоненты
│   │   ├── features/               # UI-функции и экраны предметной области
│   │   ├── routes/                 # Маршруты desktop web-приложения
│   │   └── styles/                 # Tailwind entry point и глобальные стили
│   ├── public/                     # Статические frontend-ресурсы
│   ├── e2e/                        # Зарезервировано для будущих Playwright-сценариев
│   ├── package.json
│   ├── package-lock.json            # Зафиксированные npm-зависимости
│   └── vite.config.ts
├── deploy/
│   ├── compose/                    # Docker Compose для базового развёртывания
│   └── docker/                     # Dockerfile и связанные build-артефакты
├── docs/
│   └── design/                     # Настоящие дизайн-документы
├── tools/                          # Кроссплатформенные Node-утилиты для build pipeline
├── .github/
│   └── workflows/                  # Проверки GitHub CI
├── Taskfile.yml                    # Единые команды build, generate, lint, test и format
├── .node-version                   # Node.js 24 LTS для frontend-инструментов
└── README.md
```

## Границы ответственности

- `cmd/server` не содержит предметной логики: он только собирает зависимости,
  создаёт signal-aware parent context, запускает миграции, River workers и HTTP
  server именно в этом порядке.
- `api` преобразует HTTP-запросы и ответы; Huma DTO не должны использоваться в
  persistence и integrations. Этот слой также содержит общие HTTP middleware,
  request ID, panic recovery и live/ready handlers.
- `service` координирует use cases через интерфейсы технических слоёв и не
  зависит от конкретных HTTP-деталей.
- `persistence` владеет Bun и PostgreSQL. Только этот слой выполняет доступ к
  доменным таблицам и транзакции.
- `jobs` содержит инфраструктуру асинхронного выполнения River; фактическую
  прикладную работу workers запускают через `service`. River получает общий с
  Bun `*sql.DB` через `riverdatabasesql` и отдельный listener pool через
  `NewWithPgxListener`.
- `integrations` инкапсулирует внешние HTTP API, allowlisted release catalog и
  lifecycle managed `ffmpeg`/`ffprobe`/`fpcalc`;
  provider-специфичные структуры не выходят за его границы.
- `settings` владеет настройками, сохраняемыми в PostgreSQL, metadata
  чувствительности/маскирования secrets, динамическим log level и первичной
  настройкой через Setup Manager. `service`/`api` оркестрируют каталог и managed
  tool installation lifecycle; `integrations/tools` не является UI или хранилищем
  конфигурации.
- `publication` владеет файловой системой управляемой публикации; исходные
  директории читаются через `service` и `integrations` и не изменяются без
  отдельной разрешённой настройки.
- `static` получает результат production-сборки `frontend/` и встраивает его в
  Go-бинарник. Это обеспечивает единый app-артефакт без обязательного Nginx;
  hashed assets и HTML получают разные cache policies.

## Сборка и генерация API-типов

Go собирает backend штатными командами Go. Node.js 24 LTS и npm используются
только для frontend-зависимостей и Vite/Orval/Biome/Vitest. Task является
единым кроссплатформенным интерфейсом monorepo: `task build`, `task test`,
`task lint`, `task format`, `task generate` и `task verify`. Build-артефакты
находятся в игнорируемом `build/`; staging Vite assets рядом с Go embed также
не коммитится.

Huma-код генерирует OpenAPI-спецификацию. Задача `task generate`
экспортирует её и запускает Orval, генерирующий TypeScript-клиент, React Query
hooks и mock-сценарии в `frontend/src/api/generated/`. Эта команда входит в
локальные и CI-проверки, чтобы frontend не использовал устаревший контракт.

## Миграции

`backend/internal/migrations/migrations.go` объявляет `embed.FS` через
`//go:embed *.sql` и вызывает `Migrations.Discover(files)`. Рядом с ним лежат
versioned SQL-пары, например:

```text
20260926180000_create_albums.tx.up.sql
20260926180000_create_albums.tx.down.sql
```

`cmd/server` создаёт `*bun.DB`, запускает `Migrator.Init` и применение только
неприменённых миграций с `WithMarkAppliedOnSuccess(true)`, затем запускает River
и HTTP server. Embedded SQL делает собранный Go-бинарник самодостаточным для
Docker-образа: отдельные SQL-файлы в образе не нужны.
