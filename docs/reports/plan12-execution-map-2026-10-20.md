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
  Полный `task verify` и commit hook прошли, коммит отправлен. CI `39500956951`
  завершён с `success`: verify, полная платформенная матрица, Compose smoke и
  контейнер прошли. Windows fixture correction подтверждена этим run.
  Confident matching producer, improvement/rewrite, assets journal, source cleanup,
  cross-process recovery и полная acceptance matrix остаются незавершёнными.

### Последующее уточнение 2026-10-21

- `cd0d0d5` — отчёт и статус плана. CI `39501316187` полностью завершён с
  `success`, включая платформенную матрицу, Compose smoke и контейнер.
- `8ade812` — original-owner cleanup пустого private directory после
  `directory_created` и до попытки создания input. Capability seal исключает
  новую работу; intent/result предшествуют guarded failed settlement.
  Проверены native identity/empty-directory boundary, journal transitions,
  scope exclusion и runtime source-open failure. На Windows тест требует
  удаления directory, неподтверждённого cleanup intent и retained ownership
  при unsupported directory sync. Полный gate и отдельный commit hook прошли;
  коммит отправлен, CI `39503767661` выполняется. ACK-loss/crash boundaries,
  populated-hold deletion и barrier-controlled concurrency ещё не доказаны;
  cross-process recovery не добавлен.
- `cda0527` — manual matching confirmation транзакционно создаёт publication
  requests перед receipt; оба materialization paths собирают actual touched
  track IDs. Проверены preview без requests, receipt replay и rollback при
  ошибке receipt. Shared-track/frozen-path impact expansion пока не имеет
  независимого сценария. Пустые ранее допустимые confirmations сохранены.
  Полный commit hook прошёл; коммит отправлен, результат CI пока не получен.

Эти результаты не закрывают план №12 и не заменяют реальную media/Navidrome
acceptance или оставшуюся lifecycle-интеграцию assets/source cleanup.

### Дополнение по последующим проверкам 2026-10-21

- CI `39503767661` для `8ade812` полностью зелёный: verify, вся платформенная
  матрица, Compose smoke и контейнер, включая Windows fail-closed cleanup.
- CI `39504288128` для отчёта `4b0487f` также полностью зелёный.
- CI `39503971949` для `cda0527` упал по package-wide timeout persistence
  на Windows (600 s). Полный goroutine dump показывает продолжающуюся работу
  восьми test slots, CREATE DATABASE/connection churn и queued parallel tests;
  application/fixture deadlock этим dump не выявлен. Остальные jobs прошли,
  контейнер пропущен. Run не считается зелёным. Готовится отдельное CI-only
  partitioning suite; ускорение DB provisioning не доказано.
- `9eb593b` сохраняет position lyrics authority/revision в frozen audio inputs
  и повторно сравнивает authoritative snapshot при admission. Это evidence,
  не разрешение скрыто применить manual pending lyrics. Полный gate и hook
  прошли; коммит отправлен, CI `39506052015` выполняется.

Незакоммиченная интеграция initial lyrics journal/staging/commit/recovery и
warning projection ещё не завершена и не является проверенным runtime result.

### Последующий результат 2026-10-21: фиксация интеграции

- CI `39506052015` для `9eb593b` завершился полностью успешно, включая
  Windows, всю матрицу, Compose smoke и контейнер.
- `fb5324b` — отдельный четырёхфайловый CI-only коммит: Windows persistence
  suite разделён на две детерминированные части с проверкой полноты и
  непересечения; остальные backend packages выполняются один раз.
  Штатный полный hook прошёл, коммит отправлен; CI `39575910251` ещё ожидается.
  Это разделение test budget, не доказанное ускорение PostgreSQL.
- `dcb67a1` — отдельный двухфайловый коммит copied-input SHA-256: digest
  вычисляется через owned read handle до `input_ready`; regression assertions
  проверяют digest и отдельно source/copy size/mtime. Полный hook прошёл,
  коммит отправлен; CI `39576790923` ещё ожидается.
- `5066f90` — совместная активация initial lyrics writer и обязательных
  acquisition/outcome guards, native single-use rebuild с повторным frozen
  transform, commit/recovery binding и nullable current-generation projection.
  Manual pending value/empty подавляет автоматическую подмену и не применяется
  скрыто. Projection сохраняется после settlement; generation-changing writers
  очищают initial projection до unknown.
  Полный `task format && task verify` и отдельный штатный commit hook прошли;
  коммит отправлен. Исправления partial recovery/fixtures независимо проверены;
  последние High/Medium-регрессии в этом узком review не найдены.

Эти проверки не являются полной acceptance плана: реальный media/Navidrome
прогон, cover/replacement assets, source deletion и cross-process recovery
остаются незавершёнными. CI активации ещё не подтверждён; Windows native
directory sync сохраняет fail-closed поведение при unsupported операции.

### Подтверждение CI и последующих регрессий 2026-10-21

Позднейшие результаты уточняют статусы «ожидается» выше. Полностью успешны
verify, платформенная матрица, Compose smoke и контейнер во всех следующих runs:

| Ревизия | CI run |
|---|---|
| `fb5324b` | `39575910251` |
| `dcb67a1` | `39576790923` |
| `5066f90` | `39577422729` |
| `f3a9539` | `39577841909` |
| `a6c32bc` | `39578685798` |
| `8a8988e` | `39579228676` |

`a6c32bc` отдельно добавляет проверку residual initial-lyrics projection при
replacement/manual replay: старая либо current-generation ненулевая projection
не принимается вместо обязательного NULL; replay не исправляет строку.
`8a8988e` отдельно проверяет сохранение валидной initial projection после
settlement и удаления attempt. Этот тест не доказывает warning-bearing case.
Оба коммита прошли штатные полные hooks и отправлены. Windows sharding прошёл
CI, но ускорение не доказано: первый Windows job занял 31 минуту.

План №12 остаётся незавершённым; зелёный CI не заменяет media/Navidrome acceptance.

### Дополнение после `32d8ed2`

Ниже — свидетельства выполнения по последующим изолированным коммитам, без
переписывания более ранних записей и без новых требований. Для каждого коммита
штатный полный commit hook (включая полный `task verify`) пройден без обхода;
CI run завершён полностью с `success`: verify, вся платформенная матрица,
Compose smoke и публикация контейнера.

| Ревизия | CI run | Содержание |
|---|---|---|
| `e4aa706` | `39584265048` | оценка зависимостей оригинального source cleanup без mutation authority (advisory) |
| `6369fb7` | `39586599508` | документальная фиксация одобренной последовательной проверки размера и `mtime` перед удалением |
| `870d205` | `39587675291` | helper удаления отдельных файлов после проверки размера/`mtime`; к lifecycle не подключён |
| `db11346` | `39589713337` | независимые race-тесты preview без увеличения таймаутов |
| `19a848f` | `39590035364` | durable schema владельца original source cleanup и её rollback |
| `5c73af3` | `39591611899` | транзакционный admission cleanup с dependency witnesses; к runtime не подключён |
| `6444a57` | `39592439250` | защита удерживаемых root/location/work идентичностей источника |
| `32d8ed2` | `39594655991` | fence заимствования источника публикацией против retained cleanup ownership; валидный baseline, regression и terminal owner release |

Уточнения к содержанию:

- `e4aa706` — только анализ зависимостей; mutation authority не выдаётся.
- `6369fb7` — правка `docs/design/decisions.md` и плана повторяет одобренное
  владельцем уточнение, новых продуктовых решений здесь не вводится.
- `870d205` — проверены совпадение размера и `mtime`, отказ при несовпадении и
  отсутствие рекурсивного удаления; wiring в cleanup lifecycle не добавлен.
- `db11346` — разделение сценариев гонки на независимые тесты; таймауты не
  повышались, focus/race assertions сохранены.
- `19a848f` — схема и rollback покрыты отдельными integration-тестами.
- `5c73af3` — admission и dependency witnesses проверены на реальном PostgreSQL;
  producer/worker не активированы.
- `6444a57` — retained root/location/work защищены; неполные или чужие owner
  записи не дают основания для unlink.
- `32d8ed2` — заимствование источника при валидном baseline по-прежнему
  допускается; regression терминального освобождения владельцем проверена.

Ограничения статуса (план не завершён):

- Worker/runtime для source cleanup не активированы; wiring helpers/admission в
  lifecycle отсутствует.
- Source link, topology, reset и execution fencing, permission-start и
  settlement recovery остаются незавершёнными.
- Service/API/UI projection начальных lyrics отсутствует.
- Cover, replacement, matching, cross-process recovery и реальная
  media/Navidrome acceptance остаются невыполненными.
- Текущая незавершённая работа по source link не должна описываться как
  завершённая.

Перечисленные коммиты изменяют только `backend/` и `docs/`; пользовательские
файлы и медиатека не затрагивались. План №12 остаётся в `todo/`: зелёный CI этих
runs — проверка конкретных ревизий, не приёмка плана. Настоящий раздел добавлен
в рабочее дерево после `32d8ed2`; отдельный docs-коммит и его CI здесь не
заявляются.

### Историческое дополнение: последующие ревизии (2026-10-21)

Раздел дополняет предыдущий и не переписывает его. Для каждой ревизии ниже
штатный полный commit hook (полный `task verify`) пройден без обхода, а CI run
завершён полностью с `success`: verify, вся платформенная матрица, Compose smoke
и публикация контейнера.

| Ревизия | CI run | Содержание |
|---|---|---|
| `108dd03` | `39605348271` | документация workflow |
| `fbca00c` | `39607289723` | разделение frontend-тестов |
| `fdb26d8` | `39607717123` | attach при source upsert |
| `396bcab` | `39608276318` | reset с учётом владельца |
| `04d2f7d` | `39608851454` | предотвращение fixture-регрессии |
| `c1acfc3` | `39609357932` | fences удаления и unlink |
| `a3ee74b` | `39610889529` | cleanup-owner fence только назначенных sources при local materialization, без блокировки omitted members |
| `c8658e8` (включает `509d763`) | `39612278390` | доставка identity-only cleanup |
| `759cfae` | `39615182117` | транзакционные evidence/execution start |

Незавершённые состояния (не являются приёмкой):

- `fca78eb` — frontier-коммит: полный hook пройден и отправлен, но CI
  `39616581820` не подтверждён как полностью успешный; статус — явно
  ожидается.
- Удаление per-item в рабочем дереве не закоммичено: изолированный полный
  `task verify` пройден (callback один раз, ошибки и commit failure на двух
  реальных файлах), но штатный hook ещё выполняется, хеша нет — коммит не
  заявляется.
- Main consumer admission/liveness guards и их тесты не проверены и не
  завершены.
- Worker/runtime и triggers, settlement recovery по-прежнему отсутствуют;
  service/API/UI для initial lyrics, cover/replacement, matching, wakes,
  cross-process и реальной media/Navidrome acceptance не завершены.
- План №12 остаётся в `todo/`; зелёный CI перечисленных ревизий — проверка
  конкретных коммитов, не приёмка плана. Новых продуктовых решений здесь нет.
- Корневой `AGENTS.md` в main ожидает сохранения; hook выполняется в отдельном
  дереве и в этом изменении не затрагивается.

Уточнение после завершения последующих проверок:
- `fca78eb`: CI `39616581820` завершился полностью с `success`.
- Per-item removal закоммичен как `5735bc9`, штатный полный hook пройден,
  коммит отправлен в `go-development`; CI `39618882354` ещё выполняется.
  Проверены отдельные случаи callback/commit failure и последовательное удаление
  двух реальных временных файлов; это не удаление пользовательской медиатеки.
- Consumer admission/liveness срез остаётся незакоммиченным: полный gate выявил
  ошибку компиляции тестовых River args, исправления проверяются отдельно.
  All-completed settlement реализуется отдельно; runtime worker не подключён.

Следующее уточнение:
- Consumer admission/liveness срез завершён коммитом `cf140e8`: штатный полный
  hook пройден, коммит отправлен. Проверены оба направления admission, terminal
  operation с живой River delivery, пять live states, older-job и step-history
  linkage; отсутствие holds само по себе не считается quiescence.
- All-completed settlement прошёл изолированный полный `task verify` и независимое
  review; его штатный commit hook выполняется поверх `cf140e8`. Settlement
  подтверждает committed completion, не разрешает новое удаление и не меняет
  inventory/source links. Это ещё не recovery/retry и не runtime activation.

Уточнение после коммитов settlement и документации:
- CI `39618882354` для per-item removal `5735bc9` полностью завершился с `success`:
  verify, платформенная матрица, Compose smoke и контейнер.
- Settlement закоммичен как `7561808`, полный штатный hook пройден, коммит
  отправлен; CI `39621112521` отслеживается.
- Инструкции и предыдущее дополнение отчёта сохранены в `e6f0a7c`, также после
  полного hook; CI `39621520324` отслеживается.
- Uncertain-failure transition пока не закоммичен и проходит полный gate/review.
  Он не разрешает replay unlink и не решает продуктовый контракт explicit retry.

Последующее подтверждение:
- `cf140e8` / CI `39620646512`, `7561808` / `39621112521` и `e6f0a7c` /
  `39621520324` завершились полностью с `success` (включая матрицу и контейнер).
- Uncertain-failure transition сохранён в `d140782`: полный штатный hook пройден,
  коммит отправлен; CI `39622433657` также полностью `success`. Ошибки callback
  и неопределённый completion commit сохраняют live recovery fences, а не
  запускают повторный unlink.
- Cancellation доказанно не начатого cleanup при OFF пока не закоммичен:
  полный gate выявил compile/fixture-дефекты его новых тестов, исправления
  проверяются. Dormant service runner реализуется отдельно без регистрации worker.

Уточнение после cancellation gate:
- Cancellation при OFF завершён как `f9203c9`, полный штатный hook пройден,
  коммит отправлен; CI `39633860649` выполняется. Проверены unclaimed/claimed
  queued и running-unstarted phases, отказ для started/completed frontiers,
  stale token/River attempt и атомарный rollback при deferred commit failure.
- Dormant runner/loader и их unit/PostgreSQL-тесты проходят изолированный полный
  `task verify` поверх этой ревизии; app/worker/enqueue wiring не добавлено.

### Дополнение после runner, explicit-retry guard и frontend-проверок

- `f9203c9`: CI `39633860649` полностью успешен, включая матрицу и контейнер.
- Dormant runner/loader сохранён в `bbdd349` после полного локального gate и
  штатного hook. CI `39643787191` завершился с failure: единственное падение
  verify — 5-секундный timeout SourcesScreen-теста списка каталогов. Все
  платформенные jobs прошли; публикация контейнера пропущена. Этот run остаётся
  failed-свидетельством, а не заменяется последующим успешным run.
- Решение владельца о допустимости отсутствующего файла только при явном
  пользовательском повторе сохранено в `e42e489`; hook и CI `39644189780`
  полностью успешны. Разрешённое отсутствие ещё не реализовано и не должно
  записываться как native unlink completion.
- `3a804e0` блокирует оба generic Retry пути original-source cleanup до mutation
  или enqueue; штатный hook пройден, коммит отправлен. CI `39649035481`
  отслеживается. Dedicated explicit-retry admission/execution пока отсутствуют.
- `0292c7a` убирает повторные full-table accessibility scans в упавшем frontend
  тесте, сохраняя role/name и все visibility assertions, без увеличения timeout.
  Focused suite: 20/20; отдельный affected case — 193 мс до и 144 мс после.
  Timeout локально не воспроизведён; эти измерения не устанавливают причину CI
  падения. Штатный hook пройден, коммит отправлен; CI `39649720036` отслеживается.
- Shared zero-frontier retirement и runner fallback поверх `0292c7a` прошли
  полный `task verify`; штатный commit hook выполняется. OFF cancellation и
  exact-claimed unstarted failure разделены закрытыми режимами. Любой mutation
  frontier запрещает retirement. Проверены changed facts, queued/running proof,
  completed/incomplete refusal, late callback exclusion и deferred rollback.
- Runtime worker/automatic trigger, explicit recovery и реальная media/Navidrome
  приёмка не выполнены. План №12 остаётся в `todo`; green CI не является приёмкой.

Уточнение после retirement hook: срез сохранён как `ce42e1b`, полный штатный
hook пройден, коммит отправлен в `go-development`. Независимое финальное review
не выявило correctness-регрессий. CI этой ревизии ещё не подтверждён.

Следующее подтверждение: generic Retry guard `3a804e0` завершил CI
`39649035481` полностью с `success`, включая платформенную матрицу, Compose
smoke и контейнер. Dormant worker adapter сохранён в `0bba8e3` после полного
изолированного `task verify` и штатного hook; CI `39651833323` отслеживается.
Adapter не зарегистрирован в app. Его unit-тесты проверяют non-ACK для blocked
duplicate и возврат ACK только после завершения executor, а не универсальную
quiescence-гарантию River. Owner-returned sequencing regression и технический
контракт проходят отдельный gate; explicit retry admission ещё не проверен.

CI `39649720036` для frontend-оптимизации `0292c7a` также завершился полностью
с `success`, включая матрицу и контейнер. Это не устанавливает исходную причину
timeout в `39643787191` и не меняет failed-статус исторического run.

CI `39650585372` для retirement `ce42e1b` полностью завершился с `success`
(verify, матрица, Compose smoke и контейнер).

CI `39651009094` для отчёта `f67369c`, attempt 1: failure до выполнения
Linux amd64 tests — `apt-get update` получил HTTP 403 для Microsoft noble
InRelease. Verify и остальные платформенные jobs прошли, контейнер пропущен.
Запрошен rerun failed job как инфраструктурного сбоя; результат ещё не подтверждён.

CI `39651009094`, attempt 2: failed Linux amd64 job успешно перезапущен,
публикация контейнера также успешна; итоговый run полностью `success`.
Первоначальный apt HTTP 403 остаётся отдельным свидетельством attempt 1.

CI `39651833323` для worker adapter `0bba8e3`: failure в macOS x64,
`TestSharedSelfHostedRateGateCanceledWaitDoesNotReserveSlot` — elapsed
169.364301 мс превысил wall-clock threshold. Verify и остальные платформы прошли,
контейнер пропущен. Диагностика и детерминизация cancellation-проверки выполняются
отдельно; это падение теста не классифицируется как инфраструктурный apt-сбой.

Rate-gate regression переведён на `testing/synctest`: cancellation выполняется
после blocked-wait handshake; reservation остаётся неизменной, следующий dispatch
занимает ровно один interval fake clock. Focused test прошёл. Промежуточный
state-only вариант не отправлялся: review обнаружил оставшуюся зависимость от
истечения реального interval. Полный hook окончательного исправления ещё предстоит.

Последующее подтверждение: `61f0f60` / CI `39652495413` полностью успешны.
Окончательное fake-time исправление rate gate сохранено в `62dcac2` после
полного штатного hook; CI `39655473032` полностью `success`, включая матрицу
и контейнер. Промежуточный state-only commit не отправлялся.

Explicit-retry schema/admission пока не закоммичены. Первый полный gate прошёл,
но независимое review выявило SQL-NULL holes в authority CHECK, утечку operation
ID при commit failure, ненужную зависимость от старой River row и пробелы mixed/
concurrent coverage. Исправления и regressions готовятся к повторному полному gate.
Это не готовый HTTP retry, consumption, absence-resolution или recovery execution.

После исправлений повторный полный `task verify` explicit-retry admission прошёл.
Проверены mixed completed/uncertain/unstarted frontiers, concurrent admission,
реальный transactional River insert/commit rollback с нулевым returned operation
ID при ошибке, admission после GC старой River row, partial-null consumption,
malformed binding и запрет reset/rebind consumed grant. SQL и production fixes
прошли повторное независимое review. Штатный commit hook ещё предстоит; runtime
registration и endpoint по-прежнему отсутствуют.

Следующие сохранённые срезы (все после полного штатного hook, без обхода):
- `bfd4e67`: explicit-retry admission и immutable grants отправлены;
  CI `39660092403` отслеживается.
- `25fbd4a`: одноразовый grant consumption в новую exact delivery claim отправлен;
  полный изолированный `task verify` и review пройдены; CI `39662003660`
  отслеживается. Старые operation/items/owners/claim не переписываются.
- `8ea527c`: native outcome classification отправлена; полный `task verify` и
  compatibility review пройдены; CI `39662596615` отслеживается. Обычный `Remove`
  сохраняет прежний error contract, missing root/parent не классифицируются как
  missing terminal leaf, а ошибочный unlink не становится native completion.
- Per-item recovery resolution пока реализуется отдельно: отдельные records,
  distinct native/authorized-absence/original-completion outcomes, без подделки
  старых mutation timestamps. Это ещё не проверенный runtime recovery.

Подтверждение CI: `bfd4e67` / `39660092403`, `25fbd4a` / `39662003660`
и `8ea527c` / `39662596615` полностью успешны, включая матрицу и контейнер.

Владелец повторно отклонил историю попыток: после полного successful recovery
обновляется актуальное состояние очистки, warning снимается, obsolete auxiliary
operations/grants/claims/resolutions удаляются. Предложение оставить исходный
failed-результат ради истории было ошибочным и не реализуется. Необходимые данные
неразрешённого выполнения удерживаются только как текущее recovery state.

Per-item resolution-срез получил первый green gate, затем изменён terminal-delete
trigger для удаления неактуальных records при settlement. Последняя версия
проходит повторный полный gate и независимое review; commit ещё не выполнен.

Повторный полный `task verify` последней версии resolution-среза успешен, включая
исправленный buffered handshake и bounded waits concurrency-теста. Production и
schema review замечаний не имеют. Штатный commit hook выполняется в изолированном
дереве. Settlement подготовлен: одной транзакцией обновить актуальный original
cleanup status и удалить obsolete auxiliary records; дополнительная миграция для
этого не требуется. Settlement ещё не реализован и не проверен.

Последующие срезы: resolution `fc05499` и owner-returned retry failure fence
`f498651` прошли штатные hooks и отправлены. Settlement `82ae849` также прошёл
полный gate/hook и отправлен: исходное состояние обновляется до succeeded,
obsolete recovery authority удаляется транзакционно, inventory/links неизменны.
Runtime activation и повторный retry после consumed-grant failure не завершены.

CI `39671747568` для `fc05499` failed на Windows: fixture замораживал `/source`,
который не является native absolute Windows root. T2 корректно отказал до callback;
это не timeout-проблема. Fixture исправляется до admission через `t.TempDir()` и
согласование configured/inventory paths. Production path validation не ослаблена.
Полный gate исправления выполняется отдельно от uncommitted dormant runner.
