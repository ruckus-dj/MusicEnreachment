# План 12 — сверка реализации 2026-10-20

Исходная ревизия: `38c57e63df7bc5854597b8e592f290534ec38f55`.
Сверка проведена по коду и плану `docs/plans/todo/12-file-publication.md`.
Это рабочая карта выполнения, не новые требования и не итоговая приёмка.
Источники решений — `docs/design/` и owner appendix A.1–A.17 плана.

## Исходная проверка

До изменений полный локальный `task verify` завершился успешно: генерация,
проверка generated drift, lint, Go integration tests, 315 frontend tests,
tools tests и production build. Это проверка исходного дерева, не последующих
изменений. GitHub CI исходной ревизии также проверен: run `39309203347`
завершён с `success` для `38c57e63df7bc5854597b8e592f290534ec38f55`.
Это не CI последующих правок. Browser/Navidrome acceptance не выполнялась.

## Состояние исходной реализации

| Область | Реализованная основа | Что ещё не завершено |
| --- | --- | --- |
| Identity/metadata/read model | UUID позиции, metadata scopes/provenance, primary artist API, publication read model | Полный UI редактирования и независимая pending authority/apply/discard |
| Первая публикация | Server preview → explicit admission/enqueue → River worker → staged output → first-absent exposure/commit/cleanup/settlement | Выбор source/stream в UI/API, полный configurable layout и collision suffix |
| Ручные lyrics | Save → Apply, private FLAC rewrite, actual outcome/receipt, cleanup/settlement | Source/LRCLIB acquisition, ручной поиск/выбор, остальные форматы и смена audio |
| Замена | Guarded same-path replacement journal/backup/commit/cleanup primitives | Общий audio upgrade/rewrite API и другой путь/reorganize |
| Recovery | Startup fail-closed barrier и read-only native inventory | Межпроцессное исключение, доказательство завершения прежнего mutator, recovery execution/rollback/settlement |
| Assets | TagLib writer/readback и native FLAC preservation tests | Provider cover → embedded fallback, manual cover authority, acquisition warnings/retry |
| Automatic | Pure versioned quality comparator/selector | Matching/local-only/metadata triggers, dirty coalescing, automatic admission/replacement |
| Source cleanup | Таблица, operation kind, source holds и reset fences | Producer/worker, borrower-aware unlink, точечный/bulk retry |
| API/UI | First preview/start, publication readback endpoint, manual lyrics Save → Apply | Primary artist controls, отображение actual/intended tags, replace/retry/delete/return-to-auto, assets/cleanup actions |
| Приёмка | Real PG/native tests и real TagLib format fixtures в `task verify` | Real FFmpeg publication E2E, crash/restart matrix, Navidrome import, browser и финальное независимое review |

## Ограничения доказательств

- Controlled command transport в publication lifecycle tests не доказывает
  поведение реального FFmpeg или отсутствие re-encode.
- `ObservePublicationRecovery` не даёт права продолжать попытку или удалять
  файлы. Отсутствие DB advisory lock не доказывает завершения прежнего mutator.
- Позднее решение владельца разрешает ждать только основной процесс команды;
  прежнее требование whole-process-tree containment не возвращается как gate.
- Real TagLib round-trip не заменяет проверку чтения тегов и synced lyrics
  настоящим Navidrome. Notices не являются юридическим одобрением distribution.
- CUE/manual segments и ReplayGain/R128 остаются согласованно отложенными.

План остаётся в `todo/`: эта сверка не закрывает P09/P10 и не сокращает scope.

## Уточнения владельца во время выполнения

- Preview может создавать временные файлы в существующей технической области
  output, но не среди опубликованных файлов. Native scope proof должен учитывать
  это размещение, а не молча переносить probes в каталоги релизов.
- Cleanup перечитывает настройку перед выполнением; выключение не позволяет
  начать ранее поставленное удаление. Начатая неопределённая mutation сохраняет
  свои fences до безопасного завершения.

Оба решения зафиксированы в `docs/design/decisions.md` от 2026-10-20.

## Реализационный срез этой сверки

Добавлены explicit primary artist controls для release/position, отображение
фактических тегов отдельно от intended tags и выбор проверенного source/stream
для первой публикации. Actual multiplicity аудиопотоков сохраняется даже при
единственном поддерживаемом кандидате; неподдерживаемый второй поток не делает
выбор первого неявным. Подтверждение связывает выбранные IDs и реальный stream
index, а admission повторяет выбор под transaction guards.

Native planning разрешает стабильный position suffix с удлинением при конфликте,
сохраняет собственное ранее закреплённое имя и не переписывает чужие файлы.
Preview показывает resolved destination; semantic manifest входит в digest без
случайных canonical keys. Изменение destination требует нового подтверждения.
Probes создаются только в существующей технической области `analysis` внутри
output, с проверкой совместимости scope и owned cleanup. Planner проверяет
native aliases только для затронутых компонентов, без квадратичного обхода всех
пар существующей медиатеки.

Добавлены native/unit, API, RTL и real-PG admission regressions. Независимые
review выявили и помогли исправить UI scope races, потерю выбранного stream при
transaction revalidation, implicit multistream fallback, отсутствие binding
конечного пути и квадратичное native probing. Финальный review среза блокеров
не выявил. Это не review всего оставшегося плана.

Полный `task verify` рабочего дерева после `24e0619` с pending first-preview
изменениями прошёл: generate/drift, lint, Go integration, 325 frontend tests,
tools tests и production build. GitHub CI pending-изменений этим не проверен.

### Отдельные коммиты и проверка

- `24e0619dde8ede75bc1331dcbe1f902db9909858` — primary artist controls и
  actual tag readback. Изолированный полный `task verify` в commit hook прошёл;
  коммит отправлен в `go-development`. GitHub CI `39487762010` завершился с
  ошибкой в прежнем `SourceInspectorNavigation.test.tsx` на macOS x64: тест
  читал подписку до passive effect. Остальные jobs проверки прошли, публикация
  контейнера пропущена. Этот run не считается зелёным.
- `a98a845dd5f46eb6fd1ea04a313226fb25d5c729` — explicit source/stream choice,
  stable native suffix, resolved destination confirmation и intended tags.
  Полный commit hook прошёл; отправлен в `go-development`, CI `39489332283`
  завершён с `success`: verify, Linux amd64/arm64, macOS x64/arm64, Windows x64,
  Docker Compose smoke и публикация контейнера прошли. Publication lifecycle
  tests по-прежнему используют controlled FFmpeg transport, не codec E2E.
- `42a46de7932a4bd7e31ee178249d27cfa80960df` — отдельное исправление тестовой
  гонки: ожидание реально созданной подписки, без sleep/увеличения таймаута.
  Полный commit hook прошёл; отправлен. CI `39490875568` завершён с ошибкой:
  macOS x64, `TestSourceScanStartupRecoveryPostgreSQL`, после retry число scan
  candidates оказалось 4 вместо 0. Подтверждена гонка теста: живой worker уже
  перечислил файлы новой попытки после commit retry. Проверка нулевого остатка
  перенесена после completion; атомарное удаление до enqueue отдельно покрыто
   persistence retry-тестом. Исправление включено в `3d72f98` (уточнение ниже);
  run не считается зелёным. Остальные jobs прошли, контейнер не публиковался.

### Уточнение после следующих изолированных коммитов

- `3d72f98` — устранены гонка scan retry assertion и teardown EventSource в
  manual lyrics UI-тесте. Полный commit hook прошёл, коммит отправлен;
  CI `39495030209` завершён с `success`: verify, вся платформенная матрица,
  Compose smoke и публикация контейнера прошли (уточнение 2026-10-21).
- `ff2bc03` — durable generation-scoped requests/outbox, operation-ID-only delivery,
  claim token fencing, ограниченный dispatcher без starvation и reset guards.
  Полный commit hook прошёл, коммит отправлен. CI `39495247904` завершился
  с ошибкой на macOS x64: timeout 5 s в SettingsScreen-тесте keyboard focus
  двух dialogs; конкретная задержавшаяся фаза не установлена. Остальные jobs
  прошли, контейнер пропущен; run не считается зелёным (уточнение 2026-10-21).
  Это foundation: domain producers и runtime worker ещё не подключены.
- `b82ac88` — streaming original-front Cover Art Archive adapter и source-first /
  exact-LRCLIB lyrics resolver; дробная длительность lookup сохраняется без
  округления. Полный commit hook прошёл, коммит отправлен. CI `39495478712`
  завершён с `success`: verify, вся платформенная матрица, Compose smoke и
  контейнер прошли (уточнение 2026-10-21). Acquisition helpers
  пока не подключены к publication staging/outcome journal; успешное embedding
  assets и runtime Navidrome acceptance этим коммитом не доказываются.

План остаётся в `todo`: готовые foundation/helpers не означают завершения
автоматической публикации или assets lifecycle.

### Продолжение 2026-10-21

- `a3f011a` — local confirmation пишет reconciliation intent в своей транзакции;
  внутренний automatic first admission повторно выбирает источник по
  authoritative snapshot и атомарно ACK-ает delivery вместе с admission.
  Проверены stale-guard rollback, успешный claim/ACK с сохранением новой dirty
  generation, unchanged manual path и смена authoritative settings/winner.
  Полный `task verify` и изолированный commit hook прошли; коммит отправлен,
  CI `39497152710` завершился с ошибкой в Windows x64 backend integration;
  конкретный сбой исследуется, остальные jobs прошли, контейнер пропущен.
  Run не считается зелёным. Runtime consumer в этом коммите не подключён.
- `7bdc223` — parking сохраняет unsatisfied dirty intent, release возвращает
  operation в queued, dispatcher повторно проверяет parking под request lock,
  retained recovery attempt остаётся blocker. Полный gate и hook прошли;
  отправлен. CI `39498317260` упал в Windows x64 на том же Unix-only
  `"/source"` в service fixture; остальные jobs прошли, контейнер пропущен.
  Исправление fixture на `t.TempDir()` подготовлено следующим срезом;
  этот run не считается зелёным.
- `d576c25` — opt-in optional lyrics writer различает verified/absent/mismatch
  только после успешной записи и полного required readback; ordinary Write
  не ослаблен. Полный gate и hook прошли; отправлен. CI `39498542294` упал
  на той же Windows-incompatible service fixture (`"/source"`); остальные jobs
  прошли, контейнер пропущен. Run не считается зелёным; fixture исправлена
  следующим runtime-срезом, проверка Windows остаётся за CI.
  Staging/outcome journal пока не используют этот новый boundary.
- `8e33659` — compound Settings keyboard focus test разделён на независимые
  move/install cases без повышения timeout и без удаления focus assertions.
  Полный hook прошёл; отправлен. Это устранение общего бюджета двух flows,
  не доказательство конкретной причины задержки runner в прежнем CI.
  CI `39498763452` завершился с той же ошибкой Windows source-root fixture;
  остальные jobs прошли, контейнер пропущен. Run не считается зелёным.
- `00b5bee` — consumer и River worker/periodic dispatcher подключены для
  первоначальной публикации; startup dispatch читает только durable events,
  discovery sweep отсутствует. Реальный PG-тест проходит путь request →
  dispatch → claim → consumer → admission/ACK (без запуска аудио execution).
  Blocked initial/replacement intent не ACK-ается и не парковается по stale
  snapshot: до полного wake/revalidation протокола сохраняется через retry.
  Busy delivery retry не освобождает чужой claim; cleanup cancellation-independent.
  Включено исправление Windows service fixture на платформенный temp path.
  Полный `task verify` и commit hook прошли, коммит отправлен; CI ещё не завершён.
  Confident matching producer, improvement/rewrite, assets journal, source cleanup,
  cross-process recovery и полная acceptance matrix остаются незавершёнными.
