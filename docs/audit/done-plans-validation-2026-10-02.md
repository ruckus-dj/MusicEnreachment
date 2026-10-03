# Аудит завершённых планов на 2026-10-02

## Область и результат

Проверенная ревизия: `1d4889a6669ce5c6dae848c93c66913aa0c30e14`.
До проверки и после `task verify` рабочее дерево было чистым. Ссылки на строки
ниже относятся к этой ревизии. Исправления production-кода не выполнялись.

Прочитаны все пять планов в `docs/plans/done/`, сопоставлены их требования
с реализацией и существующими тестами. Поздние изменения из планов 04/05
учтены: исторические ограничения ранних этапов не предъявляются как запреты
для последующих этапов.

**Общий вердикт: нельзя подтвердить, что все завершённые планы реализованы
корректно.** Ниже зафиксированы отклонения, обоснованные исходным кодом.
Это не исчерпывающая сертификация всех возможных состояний системы.

### Выполненная проверка

`task verify` — **PASS**:

- OpenAPI → Orval и проверка отсутствия generated drift;
- gofmt, golangci-lint: 0 issues; Biome: без ошибок;
- `go test -tags=integration ./...`: все пакеты прошли, часть результатов
  использована из Go test cache;
- Vitest: 13 файлов, 143 теста прошли;
- Node: 1 тест проверки generated-контрактов прошёл;
- TypeScript/Vite и сборка Go server прошли.

Нефатальные предупреждения: experimental localStorage в Node и размер
frontend chunk свыше 500 kB. Сами по себе они не доказывают нарушения планов.
Дополнительные локальные тестовые запуски и платформенные сборки не выполнялись.

Найденные конкурентные сценарии **не воспроизводились отдельными runtime
reproducer-тестами в этом аудите**. Для каждого указаны последовательность,
причина в коде и необходимая регрессия. PASS существующего gate не доказывает
отсутствие гонок, для которых нет соответствующих тестов.

## Отклонения

Приоритеты: **P1** — нарушение файловой изоляции либо несогласованность managed
files и БД; **P2** — ошибочный отказ, неверный snapshot, небезопасное подтверждение
или неработающий rollback в допустимом сценарии.

### A01. P1 — delete installation может удалить файлы не в текущем tools root

**План:** 02, запрет конфликтов delete/move и удаление exact managed files
(`02-setup-manager-and-managed-tools.md:187,255–256,594–596`).

**Код:** `backend/internal/service/installations.go:115–126`;
`backend/internal/persistence/setup_manager.go:359–400`.

Service читает tools root до repository transaction и захватывает его в
filesystem callback. Repository блокирует операции и проверяет active move,
но не проверяет, что захваченный root всё ещё актуален.

Последовательность:

1. Delete читает root A и останавливается до repository transaction.
2. Move завершает перенос в B и становится terminal.
3. Delete начинает transaction; active move уже нет.
4. Callback удаляет файлы по A, затем удаляется installation row.

В B остаются неучтённые executables. При сохранении старых файлов после move
также может удалиться оставленная в A копия вместо текущей installation.

**Исправление:** получать/сверять root под общей с move транзакционной защитой;
callback должен использовать защищённое актуальное значение, а не чтение
до transaction. **Регрессия:** barrier между чтением root и delete transaction,
полностью завершённый move в этом окне, проверка файлов и БД после delete.

### A02. P1 — прямая смена tools root не атомарна с началом install

**План:** 02, после появления installations tools root меняется только через
move (`02-setup-manager-and-managed-tools.md:255–256,518–520`).

**Код:** `backend/internal/service/setup.go:185–214,239`;
`backend/internal/settings/settings.go:285–333`;
`backend/internal/persistence/settings.go:43–74`;
`backend/internal/jobs/install_worker.go:102–107`.

`SaveRuntime` отдельно читает settings, отдельно проверяет отсутствие
installations, затем вызывает `SetMany`. Запись tools root не использует общую
с enqueue/move блокировку и не повторяет проверку installations в transaction.

Запрос A → B может увидеть пустой список installations; затем start install
создаёт target/job для текущего A; после этого первый запрос сохраняет B.
Worker заново читает settings. Если он уже прочитал A, файлы могут появиться
в A при settings=B; если ещё не прочитал, install выполнится в B, а не в root
проверенного preflight. Запрет обхода move нарушается в обоих случаях.

**Исправление:** проверку отсутствия installations и смену root выполнять
атомарно под общей с tools operations защитой, повторно сверяя текущий root.
**Регрессия:** конкурентные Setup runtime update/start install с управляемым
барьером, включая оба порядка чтения root worker-ом.

### A03. P2 — собственные filesystem probes вызывают ложный отказ Setup

**План:** 02, проверка writable/empty output и финальное завершение Setup.

**Код:** `backend/internal/settings/filesystem.go:83–90,113–123,139–143`;
`backend/internal/service/setup.go:159–174,221–224,391–394`.

`ProbeWritableEmpty` отвергает любое содержимое output. Другой запрос
`Setup.State`, сохранения runtime или Complete в это время может создавать
в том же output временный write-probe либо semantics directory. Общей
сериализации этих циклов нет.

Два запроса из разных вкладок либо GET одновременно с Complete могут получить
`output directory is not ready`/отказ Complete исключительно из-за временного
probe другого запроса, хотя output пользователя пуст и доступен.

**Исправление:** сериализовать полный цикл empty/writable/semantics проверки
одного нормализованного пути. Не игнорировать произвольные файлы по служебному
префиксу: это скрывает чужое содержимое. **Регрессия:** удержать semantics probe
до cleanup и параллельно проверить State/Complete/SaveRuntime.

### A04. P2 — rollback инвентаря не проходит при сохранённых scan operations

**План:** 03, обратимая миграция инвентаря; также общий контракт миграций плана 01.

**Код:**
`backend/internal/migrations/20261003000000_source_inventory_first_slice.tx.down.sql:17–24`;
`backend/internal/service/source_scan_start.go:128–139`.

Down migration сначала возвращает CHECK `operation_target_identity`, требующий
непустой `input_snapshot.target_identity` для всех kinds, кроме move, и лишь
после этого удаляет `scan_source` rows. Production scan snapshot не содержит
`target_identity`. PostgreSQL проверяет существующие rows при добавлении CHECK:
rollback падает до DELETE даже при terminal scan operation.

**Пробел проверки:**
`backend/internal/persistence/source_inventory_integration_test.go:293–340`
применяет миграцию и откатывает её без сохранённых scan rows, поэтому PASS
этого теста не покрывает рабочую БД с историей сканирования.

**Исправление:** удалить scan operations до восстановления несовместимого
CHECK, сохранив корректный порядок зависимостей. **Регрессия:** rollback через
migrator с terminal scan operation и её production-shaped snapshot.

### A05. P2 — подтверждение удаления source root устаревает до DB lock

**План:** 03, явное подтверждение path и количества locations
(`03-source-inventory-first-slice.md:151–153`).

**Код:** `backend/internal/service/source_roots.go:173–190`;
`backend/internal/persistence/source_repository.go:179–195`.

Service сверяет path/count вне delete transaction. Repository под root lock
проверяет только отсутствие active operation. Между service-проверкой и lock
PATCH может изменить configured path; scan может завершиться и изменить
количество locations. Затем delete удаляет уже другое состояние root по
устаревшему подтверждению.

**Исправление:** передать подтверждённые path/count в persistence и сравнить
оба значения после блокировки root, в той же transaction, что и delete.
**Регрессия:** barrier перед delete transaction; конкурентный path PATCH либо
завершение scan; ожидается отказ подтверждения без удаления данных.

### A06. P1 — scan следует подменённому symlink в родительском каталоге

**План:** 03, не обходить directory symlinks и не выходить за root
(`03-source-inventory-first-slice.md:166–167,184–190`).

**Код:** `backend/internal/service/source_walk.go:67–90`;
`backend/internal/service/source_scan.go:180–183,274–283`.

Walker проверяет тип entry, но следующие ReadDir, Lstat и ffprobe используют
обычный pathname. `Lstat` запрещает следование только последнему компоненту,
не symlink-предкам. Уже проверенный каталог можно заменить ссылкой на внешний
каталог между чтением entry и рекурсией/probe. Последующие чтения идут вне
root; итоговый `sourceScanConfirmFile` также не проверяет symlink-предков.

Если внешний объект совпадает по size/mtime, результат может быть принят;
для самого нарушения границы чтения совпадение metadata не требуется.

**Исправление:** root-scoped открытие с запретом symlink/traversal и безопасная
передача открытого источника ffprobe. Проверка цепочки до/после полезна, но
сама по себе не закрывает TOCTOU открытия. **Регрессия:** временное дерево,
barrier после чтения directory entry, подмена родителя ссылкой вне root;
внешний объект не должен читаться или попадать в candidates.

### A07. P2 — проверка пересечения managed/source путей пишет в source

**План:** 03, не создавать source каталог и не писать probe-файлы в него
(`03-source-inventory-first-slice.md:141–144`).

**Код:** `backend/internal/service/source_roots.go:252,259`;
`backend/internal/settings/filesystem.go:53–61,131–174`.

`checkManagedOverlap` вызывает `PathsOverlap(source, managed)`. Когда пути
пересекаются только после case folding, helper вызывает
`ProbeFilesystemSemantics` на первом существующем каталоге первого пути —
обычно самом source. Probe создаёт временный каталог и файлы.

Например, разные существующие `/data/Music` (source) и `/data/music` (managed)
на case-sensitive FS приводят к записи в source в ходе регистрации или
повторной валидации. Последующий cleanup не отменяет нарушение read-only.

**Исправление:** source validation не должна использовать write-based probe
в source; сведения о filesystem semantics получать безопасным способом
вне source либо применять read-only проверки. **Регрессия:** case-related
source/managed paths с наблюдением создания файлов, а не только сравнением
содержимого после cleanup.

### A08. P2 — detail инспектора смешивает разные состояния БД

**План:** 05, identity/result/active-operation read-model и атомарная публикация
(`05-source-technical-analysis-and-inspector.md:160–161,371–377,392–395,445–448`).

**Код:** `backend/internal/service/source_location_detail.go:100–145`;
`backend/internal/persistence/source_analysis.go:118–139,302–306`.

Root, location, variant и active operation читаются отдельными запросами
без общей snapshot transaction. GET может прочитать location без variant;
первый analysis затем атомарно публикует variant и становится succeeded;
чтение active operation уже возвращает nil. Клиент получает `not_analyzed`
без operation ID для подписки и не узнаёт о готовом результате без нового
REST-read по иной причине.

Другой случай: после чтения location с variant A конкурентный apply/scan
отвязывает A. Если ссылок из сохранённых operation rows уже нет, orphan cleanup
удаляет A до `GetMediaVariant(A)` и detail завершается ошибкой, которую API
отдаёт как 500. **Уточнение:** свежая succeeded analysis operation может
удерживать A; не любой повторный анализ немедленно удаляет старый variant.

**Исправление:** собирать detail одним согласованным persistence query либо
в read-only `REPEATABLE READ` transaction, включая active operation.
**Регрессия:** barrier после location read; конкурентный first apply и
unlink/cleanup старого variant. Допустим согласованный старый либо новый
snapshot, но не смешанное состояние/500.

### A09. P1 — pre/post Lstat анализа не защищает фактическое открытие ffprobe

**План:** 05, путь без directory/file symlink и выхода за root; symlink
replacement не публикует результат
(`05-source-technical-analysis-and-inspector.md:300–313`).

**Код:** `backend/internal/service/source_analysis.go:140–174`;
`backend/internal/service/source_analysis_path.go:31–49`;
`backend/internal/integrations/tools/ffprobe_analysis.go:32–39`.

В отличие от scan, анализ проверяет всю цепочку Lstat, но ffprobe позднее
сам открывает обычный pathname. Между pre-check и открытием файл/родитель
может быть заменён symlink на объект вне root, а до post-check исходный
объект возвращён на место. Probe читает внешний объект, post-check видит
прежние size/mtime и разрешает публикацию чужих технических данных.

Это не оговорённая stat-модель изменения bytes без size/mtime: исходный
объект возвращается неизменным после чтения другого объекта.

**Исправление:** безопасно открыть источник относительно закреплённого root
с запретом symlink/traversal и дать ffprobe читать закреплённый открытый
объект через поддерживаемый платформой механизм. Повторный Lstat либо
сравнение inode после чтения не устраняют окно открытия. **Регрессия:**
временная подмена перед фактическим чтением probe и восстановление оригинала
до post-check. Существующий `source_analysis_stale_test.go:124–186` проверяет
подмену, оставшуюся до post-check, но не этот сценарий.

## Что не является отклонением

- Предметные endpoints/таблицы/workers после плана 01 — дальнейшее развитие,
  а не нарушение исторического scope технического фундамента.
- Отсутствие SHA-256, fingerprint, quality scoring, matching, staged processing
  и публикаций не является недоделкой этих пяти планов: они вынесены за scope.
- `media_variant` и технический инспектор, исключённые ранними планами,
  введены отдельно планом 05.
- Исправления Windows path constraint, root unavailable, terminal failure
  refresh и diagnostic mutation gates из предыдущего аудита рассмотрены
  с учётом плана 04, а не повторно объявлены отсутствующими.
- Историческое `--version` в плане 01 не требует менять production-код:
  план 04:119–132 явно утверждает `-version` и запрещает переписывать старые
  завершённые планы задним числом.
- BtbN `latest` с numbered asset identities и сохранение publication evidence
  при recovery — документированные уточнения плана 02.
- Compose CI smoke сохранения Setup settings не является полным завершением
  Setup или Windows+PostgreSQL E2E; эти проверки нельзя объявлять выполненными
  только по наличию smoke job.

## Карта сверки пяти планов

| План | Сопоставленные области | Результат |
| --- | --- | --- |
| `01-development-foundation.md` | monorepo/toolchain, Task/CI/pre-commit, Compose, startup migrations → River → HTTP, embedded static/cache, API generation, health/lifecycle | Gate прошёл; отдельного нового дефекта фундамента не найдено. A04 нарушает унаследованный контракт обратимости миграций. Чистый checkout и отдельный Compose smoke в этом аудите не запускались. |
| `02-setup-manager-and-managed-tools.md` | Setup/completion/route gates, path probes, MusicBrainz generation check, catalog/preflight, install/activation/delete/move, workers/recovery, SSE/REST, retention, frontend bootstrap/errors | A01–A03. Остальные просмотренные области не дали новых доказанных дефектов; все crash windows и реальные upstream загрузки не сертифицированы. |
| `03-source-inventory-first-slice.md` | schema/rollback, candidates/apply, unchanged IDs/stale inventory, CRUD/confirmation, read-only walk/symlinks, bounded probe, enqueue/worker/retry/recovery, API, pagination/terminal refresh/UI | A04–A07. UI/API просмотрены с учётом последующих изменений 04/05. |
| `04-source-inventory-design-alignment.md` | Windows path migration и platform boundary, unavailable/fencing, классификация root/file/tool/cancellation, refresh после failure, diagnostic gates, документация `-version` | Новых отдельных отклонений этого корректирующего этапа не найдено. Ручной browser/deploy-smoke и Windows CI не повторены; это не безусловный COMPLETE. |
| `05-source-technical-analysis-and-inspector.md` | variant schema/up/down, apply/cleanup/idempotence, bounded technical ffprobe, nullable normalization/tags/raw JSON, path/stat checks, pinned enqueue/holds, worker/recovery/retry, detail API, inspector/navigation/subscriptions | A08–A09. Ручной анализ реальным managed ffprobe и viewport acceptance не повторены. |

Проверены, в частности, реализации и/или тестовые свидетельства в:

- `backend/internal/service/setup_completion_integration_test.go`,
  `backend/internal/api/setup_test.go`,
  `frontend/src/features/setup/OperationProgress.tsx`,
  `frontend/src/features/settings/SettingsScreen.tsx`;
- `backend/internal/api/register.go`, `backend/internal/service/operations.go`,
  `backend/internal/jobs/cleanup_worker.go`, `backend/internal/jobs/reconcile.go`,
  `frontend/src/api/client/operations.ts`;
- `backend/internal/persistence/source_inventory_integration_test.go`,
  `source_repository_integration_test.go`, `source_root_path_platform_integration_test.go`,
  `backend/internal/api/sources_test.go`, `sources_integration_test.go`,
  `backend/internal/jobs/scan_recovery_integration_test.go`;
- `frontend/src/features/sources/SourceScanControl.tsx`, `SourceDetailScreen.tsx`,
  `SourceLocations.tsx`, `SourcesScreen.tsx` и соседних RTL-тестах;
- `backend/internal/persistence/media_variant_schema_integration_test.go`,
  `source_analysis_integration_test.go`,
  `backend/internal/service/source_analysis_stale_test.go`,
  `backend/internal/integrations/tools/ffprobe_analysis_test.go`,
  `backend/internal/jobs/source_analysis_recovery_integration_test.go`,
  `frontend/src/features/sources/SourceInspectorScreen.tsx`, `useSourceInspector.ts`.

Это карта свидетельств, не заявление о полном построчном прочтении каждого
перечисленного тестового файла.

## Ограничения доказательств

Проверка исходников и существующих тестов охватывает все пять документов,
но не все тела тестов и не все потенциальные crash/concurrency interleavings.
В этом аудите не повторялись ручные browser-сценарии, viewport screenshots,
полный first-run Setup с реальными upstream downloads, production restart
с реальными managed tools и Windows/macOS/Linux CI matrix. Исторические
заявления о ручной приёмке в планах не превращены в независимо подтверждённые
результаты этой проверки. Отсутствие такого повторения — ограничение аудита,
не автоматически доказанный дефект реализации.

Для закрытия замечаний нужны исправления, указанные детерминированные
регрессии и повторный `task verify`. Файлы завершённых планов и предыдущий
аудит этим отчётом не изменяются.
