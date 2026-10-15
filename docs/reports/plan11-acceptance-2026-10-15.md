# План 11 — итоговые проверки matching

Дата: 2026-10-15. Проверенная implementation/UI-ревизия:
`f36ac8fb79ce4c73cf0202881595129e08ab39db`.

## Локальный gate

- `task verify` завершился успешно: generation/drift, gofmt, golangci-lint,
  Biome, Go unit/integration с PostgreSQL/River fixtures, 298 frontend tests,
  Node tools tests, frontend/backend build.
- Pre-commit повторил полный gate успешно перед созданием `f36ac8f`.
- Исправлены выявленные проверками неверный SQL join локальных тегов, потеря
  nested override provenance, запрет повторного manual после собственного
  authority transition, multidisc UPDATE без medium и ошибки test fixtures.
- Integration tests подтверждают additive sources, сохранение ID/selected-state
  существующей source-связи, явные пустые теги, отсутствие provider-связей у
  локальных релизов, сохранение пропущенных позиций, stale fences и receipt replay.
- Автоматический runtime существовал до этого increment. Его regression tests,
  включая post-analysis/reconciliation/provider-wake/apply, выполнялись общим
  gate; этот отчёт не объявляет прежний pipeline «неподключённым».

## Browser-приёмка

Production frontend из успешного gate обслуживался через Vite preview на
`http://127.0.0.1:5174`. Playwright/Chromium, API замокирован по generated DTO;
новые зависимости и mock endpoints в приложение не добавлялись.

Во всех четырёх сочетаниях **1440×1000 / 390×844 × light / dark** выполнены:

1. Выбор provider release и назначение двух файлов одной позиции без replacement
   dialog; явное `title_override: ""` присутствует в preview payload.
2. Focus assignment select и запуск provider preview клавишей Enter.
   Значение native select задавалось Playwright `selectOption`; это не
   свидетельство прогона нативного popup стрелками. Drag/select alternatives
   дополнительно покрыты RTL.
3. Создание локального релиза с пустыми title/artists, явно введёнными medium=2,
   position=3 и двумя выбранными файлами; preview и confirm.
4. Явная загрузка local catalog и выбор существующей позиции; старый source
   отображается отдельно, один новый member добавлен через preview/confirm.
   `source_media_ids` не отправляется как доверенный input.
5. Merge → server preview с replacement group ID → split этого ID → отдельное
   подтверждение. Во втором запросе сохранён первый action.
6. Просмотр backend-shaped `release_profile` / `assignment_profile` и `Factors`,
   raw/normalized values и provenance, без вычисления score в браузере.

Результат финального прогона: **PASS**, отсутствуют page/console errors,
незамокированные API requests и горизонтальное переполнение документа.
Снимки просмотрены основным агентом. Dev-overlay React Scan в финальном
production-прогоне отсутствует.

### Локальные артефакты

Каталог:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan11-browser-acceptance/`.

- `plan11.mjs` — временный harness;
  SHA-256 `0145f158bc2584b7397a36f3f0f2642149d1bfd0fdc1e813a48427f487b15a40`.
- `evidence/browser-report.json` — запросы и результат;
  SHA-256 `1f07003b6f474a71176038c7c9538d77e88a2e6e86e95bf4d6105c0b0d4ab273`.
- `evidence/light-desktop.png`, `light-mobile.png`, `dark-desktop.png`,
  `dark-mobile.png` — финальные четыре screenshots.
- Финальный stdout: соседний файл `plan11-browser-production-accepted.log`.

Ранние прогоны harness не проходили из-за перехвата Vite source URL как API,
неточных selectors, повторного закрытия details и автоматизации native select.
Они не учитываются как успешная приёмка; `failure.png` — промежуточный артефакт,
не финальный screenshot. Production UI при этом не менялся.

## Независимое review

Специализированные read-only reviews проверили local transactions/schema,
provider overrides/current-state fencing, additive behavior, UI lifecycle и
соответствие latest owner addenda. Найденные P1/P2 исправлены и покрыты
регрессиями. Финальное review `f36ac8f` не нашло implementation-блокеров.

Подтверждённые области: pure-local catalog без provider links, explicit empty
overrides, manual protection, receipt identity, повторные manual confirmations,
page-local independent drafts, grouping replacement IDs и backend evidence.
Исторические требования occupied-position dialog, matching unlink и provider
provenance локальных сборников superseded решениями владельца, не открытые gaps.

## GitHub CI

Проверка implementation commit:
[run 38698026399](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/38698026399).
На момент создания этой записи run ещё выполняется; успешный CI и завершение
плана этим разделом пока не заявляются.

### Дополнение 2026-10-15 — rollback regression и первый CI

- `a82f6f3eff25cac9e08d05f01e8d15029d5f8e22` добавил PostgreSQL-проверку
  clean rollback/reapply `20261214000000` и отказа populated rollback с
  сохранением receipt, local release и source root. Его полный pre-commit gate
  прошёл. Production/UI после `f36ac8f` не менялся.
- Run `38698026399` завершился **failure**: в `verify` race-test не дождался
  заголовка «Сохраненная оценка» за стандартную секунду RTL. Backend integration,
  Compose smoke и все пять native platform jobs прошли; publish был skipped.
  Исходный failure не считается успешной приёмкой. Точечное исправление ожидания
  тестовой готовности и повторный CI ещё ожидаются.
- Для rollback-test revision запущен
  [run 38698852637](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/38698852637).

### Дополнение 2026-10-15 — ожидание review в CI

`76d63105ac797be779ae8ccbd5f0b53ba2250581` заменил стандартное секундное
ожидание заголовка на ограниченное 5-секундное ожидание того же observable
ready-state. Sleeps и ослабление stale-response assertions не добавлялись.
Полный pre-commit gate прошёл; коммит отправлен в `origin/go-development`.
Production/UI по-прежнему соответствует browser-ревизии `f36ac8f`.

Run `38698852637` завершился failure на аналогичном секундном readiness wait
соседнего layout-test в macOS x64. Коммит
`387adff8da9a5ea02b3faaf733b8d7d5aa1be52e` применил к нему такое же ограниченное
ожидание того же заголовка; полный pre-commit прошёл. Это также test-only
изменение, не новая browser-ревизия приложения.

### Дополнение 2026-10-15 — успешный полный CI

[Run 38699113295](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/38699113295)
для `76d63105ac797be779ae8ccbd5f0b53ba2250581` завершился **success**:
`verify`, Compose smoke, Linux amd64/arm64, macOS x64/arm64, Windows x64 и
publish container image прошли. Для следующего test-only коммита `387adff`
выполняется
[run 38700046349](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/38700046349).

### Итоговое дополнение 2026-10-15

Run `38700046349` для
`387adff8da9a5ea02b3faaf733b8d7d5aa1be52e` завершился **success**: `verify`,
Compose smoke, все пять native platform jobs и publish container image прошли.
Это финальная implementation/test ревизия; production UI не менялся после
browser-приёмки `f36ac8f`. M01–M10 приняты, план перенесён в `done/` отдельным
документационным follow-up, не изменяющим проверенный runtime.

## Ограничения

Browser evidence использует mock API и не заменяет PostgreSQL/River integration
tests общего gate. Не выполнялись live MusicBrainz/AcoustID requests, публикация
файлов или проверка всех вариантов исходной медиатеки. Windows arm64 остаётся
явно неподдерживаемой платформой. Локальные screenshots/harness не хранятся в Git;
их наличие на другой машине не предполагается.
