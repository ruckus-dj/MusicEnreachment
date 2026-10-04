# План: закрыть замечания аудита завершённых этапов

## Статус и основания

Подготовлен 2026-10-03. **Статус: COMPLETE (2026-10-04).** Шаги 1–11 были
реализованы, шаг 12 завершён: независимый полный `task verify` завершился с
кодом 0, реальный ручной сценарий пройден, независимая приёмка выполнена,
полный native CI `37153819171` прошёл всеми jobs на ревизии
`a42f09fded69fbd6b90f22122392aeb27c69e7f1`. UNC/SMB исключён из этой части по
одобрению владельца; это ограничение продукта, а не blocker согласованного
scope. Основание —
`docs/audit/done-plans-validation-2026-10-02.md`, замечания A01–A09.
Ранняя повторная сверка в этом документе — исторический снимок до исправлений,
а не описание текущего состояния. Актуальные свидетельства выполнения и
независимая матрица A01–A09: `../../reports/plan06-independent-review-2026-10-04.md`,
`../../reports/plan06-acceptance-2026-10-03.md` и
`../../reports/source-io-evidence-2026-10-03.md`.

### Уточнение владельца после завершения (2026-10-04)

Решение владельца в `docs/design/decisions.md` уточняет продуктовый критерий:
размер и `mtime` достаточны для признания файла текущим; неизменность байтов и
защита от конкурентной подмены пути/ссылки не являются требованиями или
блокерами. Это новое решение не меняет историческую приёмку плана: A01–A09 и
шаг 12 остаются **COMPLETE** по тогда согласованному scope. Реализация может
оставаться строже; pinned handles, no-follow/reparse rejection, проверки
идентичности, fd transport и сопутствующие CI-проверки сохранены, но не
обязательны как продуктовый acceptance gate. Текущий отказ Windows UNC остаётся
реализационным ограничением, а не постоянным запретом; восстановление NAS-доступа
будет функциональной задачей и не требует доказательств безопасной работы с SMB.
Исходные шаги 1–5 и прежние аудиты не переписываются.

### Повторная сверка актуальности (исторический снимок до изменений)

Все девять замечаний **были подтверждены статическим анализом call paths на
ревизии аудита**.
Новые runtime reproducer-tests и `task verify` при подготовке этого документа
не запускались; PASS gate из исходного аудита не выдаётся за повторную проверку.
Ниже — карта причин на исходной ревизии, а не описание текущего состояния и не
новые результаты тестового запуска.
Таблица ниже — снимок состояния из исходного аудита **до** изменений; она не
переписана после реализации шагов.

| ID | Актуальное свидетельство в коде | Уточнение / шаг |
| --- | --- | --- |
| A01, P1 | `Installations.Delete` читает root до `SetupManagerRepository.DeleteInstallation`; callback захватывает старый root, repository его не сверяет. | После terminal move удаляются файлы в A, не в текущем B. Шаг 1. |
| A02, P1 | `SetupService.SaveRuntime` → отдельные root/list reads → `Registry.UpdateRuntime`/`SettingsRepository.SetMany`; `CreateInstallationOperationAndEnqueue` не сериализован с записью root. Worker перечитывает settings, `InstallInputSnapshot` не содержит root. | Повторный preflight fingerprint в service не закрывает окно до enqueue transaction. Шаг 2. |
| A03, P2 | `ProbeWritableEmpty` отвергает любое entry; `ProbeFilesystemSemantics` временно создаёт entries, общего guard нет. | Writers включают также `ValidatePaths`. Empty-check зависит от completion/выбранного output; lock нужен по фактическому probe directory. Шаг 3. |
| A04, P2 | Inventory down восстанавливает `operation_target_identity` до удаления `scan_source`; production `ScanSourceSnapshot` не содержит `target_identity`. | Более поздний analysis down не устраняет этот дефект цепочки rollback. Шаг 4. |
| A05, P2 | `SourceRoots.Delete` сверяет path/count до transaction; `SourceInventoryRepository.DeleteSourceRoot` под lock проверяет только active operation. | Terminal scan/PATCH до lock делает подтверждение устаревшим. Шаг 5. |
| A06, P1 | `WalkSourceTree` рекурсирует через pathname; `SourceScan.candidateFor` передаёт absolute path probe; `sourceScanConfirmFile` использует `Lstat`. | `Lstat` последнего компонента не защищает ancestor, особенно подменённый после listing. Шаги 8–10. |
| A07, P2 | `SourceRoots.checkManagedOverlap` → `PathsOverlap(source, managed)` → write-based `ProbeFilesystemSemantics` в source/его существующем предке. | Итоговая неизменность дерева после cleanup не доказывает отсутствие записи. Шаг 6. |
| A08, P2 | `SourceLocationDetails.Read`: отдельные root/location/variant/active reads; `ApplyAnalysisResult` завершает operation атомарно, cleanup может удалить уже отвязанный variant. | Для второго сценария убрать удержания A из operation rows; свежий success может удерживать A. Шаг 7. |
| A09, P1 | `SourceAnalysis.Run`: resolve → pathname `ProbeTechnical` → resolve; `FFProbe.ProbeTechnical` открывает путь позже проверок. | Transient swap с восстановлением до post-check не покрыт тестом persistent swap. Шаги 8–9, 11. |

### Ход исполнения

Таблица ниже сохраняет историю реализации шагов 1–11 и исправлений. Отметка о
статическом ревью относится к тому периоду и не заменяет завершённую независимую
приёмку шага 12, описанную в актуальных evidence reports.

| Шаг | Коммит(ы) |
| --- | --- |
| 1 | `5c0fb8b` |
| 2 | 2a: `ba37b3b`; 2b: `f6134c9` |
| 3 | `7eba315` |
| 4 | `2519ab9` |
| 5 | `2b40ef0` |
| 6 | `6eef618` |
| 7 | `3a7a365` |
| 8 | 8a: `af250da`; 8b: `1059997`; 8c: `79fb8ef`; 8d: `3ab1f57` |
| fixes | `2784703`, `cef527c`, `84a0801`, `2fb0cb1` |
| 9 | `d35a2bd` |
| 10 | `12963b8` |
| 11 | `1ca975c` |
| CI updates | `7d31194` |
| 12 / final corrections | `acad608`, `a42f09f` |

Шаг 12 завершён на полном commit `a42f09fded69fbd6b90f22122392aeb27c69e7f1`.
Независимый `task verify` — exit 0 (Go test cache использовался там, где
применимо); полный CI — все jobs SUCCESS; ручные сценарии и cleanup завершены.
Подробные имена тестов, ограничения Windows namespace acceptance и точные
манифестные версии/SHA-256 приведены в трёх отчётах, перечисленных выше.

Основания контрактов: `docs/plans/done/02-setup-manager-and-managed-tools.md`,
`03-source-inventory-first-slice.md`, `04-source-inventory-design-alignment.md`,
`05-source-technical-analysis-and-inspector.md`, а также
`docs/design/decisions.md`, `repository-architecture.md`, `deployment.md` и
`external-tools.md`. Завершённые планы и исходный аудит не переписывать.

## Пользовательский результат и границы

Удаление инструмента действует на текущие managed files; смена tools root
не обходит move при конкурентном install. Параллельные запросы Setup не
мешают друг другу собственными временными probes. Удаление source root
принимает только актуальное подтверждение. Source остаётся read-only;
scan/analysis не читают объекты через подменённые symlinks. Инспектор
показывает согласованное состояние inventory/result/operation. Rollback
инвентаря работает при сохранённой истории scan.

Вне этапа: SHA-256/fingerprint, staged mode, scratch/work-directory,
matching, публикации, новые настройки, auth/CORS, изменение продуктовых
сценариев и редизайн UI. Копирование source во временный файл ради обхода
TOCTOU не вводить: это не исправление существующего in-place контракта.
Поддержку Linux/macOS amd64/arm64 и Windows amd64 не сужать молча.

## Порядок исполнения и передача агентам

- Каждый нумерованный шаг — отдельный ограниченный change set. Исполнитель
  читает локальные `AGENTS.md`, текущие реализации и соседние тесты перед
  правкой. Имена новых методов ниже описывают контракт, а не обязательное
  именование.
- `implementer` выполняет транзакционные и платформенные изменения;
  `quick` — только локальные, уже определённые правки и обновление fixtures.
  Не отдавать `quick` проектирование блокировок или защиту от TOCTOU.
- Для каждого шага `tester` проверяет детерминированную регрессию,
  `reviewer` — инварианты и blast radius. Доказательства: файлы/имена tests,
  сценарий, ожидаемый результат, gate и оставшиеся ограничения среды.
- Единственный локальный test/build gate — **`task verify`**. Новые tests
  включать в существующие suites; отдельный запуск допускается только для
  диагностики упавшего gate. Ни `sleep`, ни многократный probabilistic run
  не заменяют barriers/channels/query hooks с timeout.
- Не менять generated-клиент вручную; если контракт действительно меняется,
  использовать существующую генерацию. API-поля этого плана сохраняются.
  Не добавлять env vars, зависимости без обоснования или `nolint`.
- Forward-миграции не переписывать. Единственное адресное исключение —
  исправление порядка действий в неисправном **down** шага 4; объяснение
  приведено там. Не распространять исключение на остальные SQL-файлы.
- Коммиты только при явном разрешении сессии исполнения. При неразрешимой
  неоднозначности остановить зависимый шаг, записать blocker и спросить
  владельца; не объявлять его COMPLETE.

## Шаги реализации

### 1. Защитить выбор tools root при delete installation (A01)

**Исполнитель:** implementer. **Зависимости:** нет.
**Область:** `backend/internal/service/installations.go`,
`backend/internal/persistence/setup_manager.go`, соответствующие interfaces/tests.

Передать filesystem callback актуальный tools root, прочитанный внутри
delete transaction под той же защитой, которую используют enqueue/move.
Не захватывать root из предварительного service-read. Сохранить проверки
active installation, active operation/read holds и move, а также удаление
только exact managed files; не заменять его `RemoveAll` каталога версии.
Проверить порядок блокировок до внедрения: новые locks не должны инвертировать
порядок существующих tools transactions.

**Проверка tester:** barrier после старого предварительного read, но до
delete transaction; полностью завершить move A → B и продолжить delete.
После удаления managed files в B и installation row отсутствуют; оставленная
по выбору оператора копия в A и чужие файлы не удалены. Проверить вариант
без сохранения старой копии и обратный порядок: delete захватил защиту раньше
move. Ошибка callback не должна удалить installation row.
**Проверка reviewer:** root и guards относятся к одному защищённому состоянию;
нет нового обхода holds/active checks.

### 2. Атомарно менять tools root относительно install/move (A02)

**Исполнитель:** implementer. **Зависимости:** 1 (общий lock contract).
**Область:** `backend/internal/service/setup.go`,
`backend/internal/settings/settings.go`,
`backend/internal/persistence/settings.go`, tools enqueue/preflight и
`backend/internal/jobs/install_worker.go`.

Вынести запись runtime settings с изменением tools root в persistence
transaction: под общей tools-защитой перечитать текущий root и повторно
проверить installations и queued/running tools operations. Только после
этого записать связанные settings; отдельный `ListInstallations` перед
`SetMany` не является защитой. Запретить устаревший update/CAS, не оставлять
частично сохранённые runtime settings при конфликте.

Передавать ожидаемый root preflight в enqueue и сравнивать с текущим
`tools_directory` внутри защищённой transaction до создания target/job:
одной повторной service-проверки fingerprint недостаточно. Сохранить
привязку preflight к root: если B записан раньше enqueue с планом
для A, start обязан отказать устаревшему preflight. Если enqueue для A
закоммичен раньше, прямое изменение root должно отказать. Worker не должен
прочитать другой root относительно принятого preflight/snapshot. Проверить
все обходные записи tools root, включая move commit и retry/recovery;
не вводить глобальную блокировку на время сетевого download.

**Проверка tester:** реальные PostgreSQL barriers между предварительной
валидацией и записью settings/enqueue, оба порядка commit. Для install,
победившего гонку, проверить worker до и после чтения root: файлы только в A,
settings=A. Для победившего update: settings=B, старый preflight не создаёт
installation/job/files. Unchanged root и изменение остальных разрешённых
settings сохраняют прежнее поведение; failure откатывает всю запись.
**Проверка reviewer:** общая защита действительно охватывает обе стороны,
включая start/retry/move; нет фикса только одного worker timing.
Этот шаг выполнять двумя последовательными инкрементами: сначала
transactional root update + общий enqueue lock/CAS с regression, затем
проверка worker/retry/recovery и обновление fixtures. Если меняется durable
snapshot, явно версионировать его и проверить старые queued/failed operations;
нельзя дописать отсутствующий root из нынешних settings и объявить его
исторически подтверждённым.

### 3. Сериализовать полный цикл Setup filesystem probes (A03)

**Исполнитель:** implementer; fixtures может обновить quick.
**Зависимости:** нет. **Область:**
`backend/internal/settings/filesystem.go`, `backend/internal/service/setup.go`.

Ввести общую для всех callers защиту по **фактическому нормализованному
каталогу probe**, охватывающую существующий empty/writable/semantics цикл
и cleanup. Все `State`, `SaveRuntime`, `ValidatePaths` и `Complete` должны
пользоваться ею, а не собственными несвязанными mutex. Для отсутствующего
output `ValidatePaths` использует `existingProbeDirectory`: его существующий
предок может совпасть с output другого запроса. Guard по одному запрошенному
несуществующему output здесь недостаточен. Проверить также прямые callers
probe helpers и semantics checks из managed `PathsOverlap`: они не должны
создавать unguarded probe в каталоге, который параллельно проверяется на empty.
Не вкладывать повторный захват того же lock в helper; определить срок жизни
keyed locks, общий порядок нескольких locks и снятие при ошибке.
Разные непересекающиеся outputs не обязаны блокировать друг друга.
Не игнорировать файлы пользователя по служебному префиксу.

**Проверка tester:** удержать semantics probe до cleanup; конкурентные
State/Complete/SaveRuntime/ValidatePaths для того же probe directory ждут
и затем оценивают пустой каталог корректно. Добавить случай отсутствующего
output с probe в существующем предке, совпавшем с output второго запроса.
Проверить настоящий чужой файл (в том числе с похожим
префиксом), ошибки cleanup/probe и повторный запрос без зависания.
**Проверка reviewer:** все production call paths пользуются одной защитой;
нормализация/алиасы не обходят её, проверка чужого содержимого не ослаблена.
Разделять незавершённый Setup (State требует empty output) и завершённый
(State проверяет writable, существующий output допустим); не распространить
empty-проверку на уже работающую медиатеку. Для `ValidatePaths` сохранить
empty только до completion либо для другого output. Регрессия: completed
Setup + непустой сохранённый output → State/ValidatePaths успешны; новый
непустой output по-прежнему отклоняется.

### 4. Исправить rollback с сохранёнными scan operations (A04)

**Исполнитель:** quick для SQL; implementer/tester для PostgreSQL regression.
**Зависимости:** нет. **Область:**
`backend/internal/migrations/20261003000000_source_inventory_first_slice.tx.down.sql`,
`backend/internal/persistence/source_inventory_integration_test.go`,
migration integration tests.

Перенести удаление `scan_source` rows **до** восстановления несовместимых
CHECK constraints; сохранить порядок удаления FK/index/exclusion dependencies.
Исправить комментарий, сейчас обещающий обратный порядок. Up-файл и
содержимое forward schema не менять. Более поздний down
`20261005000000_source_media_variant.tx.down.sql` уже удаляет `analyze_source`
до восстановления kind CHECK: сохранить это поведение.

Это ограниченное исправление исполняемой обратной процедуры, а не изменение
ранее применённой forward-миграции. Новая forward-миграция не исправит
порядок SQL внутри старого down; удалять историю scan в новой up ради
успешного rollback недопустимо. Reviewer проверяет эту адресную поправку
и фактический migrator path; не обходить migration bookkeeping ручным SQL.

**Проверка tester:** расширить настоящий migrator rollback test terminal
`scan_source` с production-shaped snapshot без `target_identity`, включая
сохранившуюся operation после удаления root. Проверить isolated inventory
up/down и цепочку analysis → platform-path → inventory rollback с допустимыми
для старой схемы Unix paths. Сохранённая tools operation и её guards остаются
корректны; scan tables/columns и rows удалены. Проверить также повторный up.
Не объявлять ожидаемый отказ старого platform-path down на Windows-пути
дефектом A04 и не ослаблять его CHECK ради теста.

### 5. Проверять delete confirmation source root под DB lock (A05)

**Исполнитель:** implementer. **Зависимости:** нет.
**Область:** `backend/internal/service/source_roots.go`,
`backend/internal/persistence/source_repository.go`, соседние API/service fixtures.

Передать подтверждённые path/count в persistence. В существующей delete
transaction после operations lock и root `FOR UPDATE` прочитать configured
path и посчитать locations в этой же transaction; сравнить оба с переданными
значениями до удаления. Сохранить запрет active scan/analysis и orphan cleanup.
Несовпадение преобразовать в существующий service confirmation conflict,
не в 500. Предварительная service-проверка может быть ранним отказом, но
не заменяет транзакционную. Новое публичное поле generation/token не нужно:
контракт подтверждает именно path и count, а не все bytes inventory.

**Проверка tester:** barrier после ранней проверки перед delete transaction;
затем отдельно завершить path PATCH или scan с изменившимся count. Delete
отказывает, root/locations/results не удалены. Обратный порядок защищён locks;
неизменённое подтверждение позволяет delete, source files остаются нетронуты.
**Проверка reviewer:** сравниваются оба значения под защитой, которая также
используется writers inventory/path; API status и fixtures не регрессируют.

### 6. Убрать записи в source при overlap validation (A07)

**Исполнитель:** implementer. **Зависимости:** нет.
**Область:** `backend/internal/service/source_roots.go`,
`backend/internal/settings/filesystem.go`, source path tests.

Выделить read-only overlap validation для source, не вызывающую write-based
`ProbeFilesystemSemantics` в source или в каталоге, пересекающемся с ним.
Использовать нормализованные пути и read-only filesystem identity/alias checks;
учитывать оба направления вложенности, case-sensitive/case-insensitive FS,
существующие и отсутствующие managed paths. Если semantics не доказана,
не делать разрешающий guess; вернуть понятный отказ неоднозначной проверки.
Не менять молча общую семантику всех managed-path callers `PathsOverlap`.

Конкретная схема read-only helper: component-aware lexical containment,
затем сравнение identity существующего root с предками другого пути в обоих
направлениях; для отсутствующего managed tail — deepest existing ancestor
и prospective suffix. Ошибки доступа/разрешения не превращать в `false`.
Подтверждённо разные существующие `Music`/`music` на case-sensitive FS
разрешать. Произвольные bind mounts/remount не объявлять покрытыми:
mount topology — доверенная deployment-граница, не новый security promise.

**Проверка tester:** раздельные case-related source/managed каталоги на
case-sensitive FS; существующие overlap/alias случаи; readonly source;
не существующий managed suffix. Проверка должна обнаруживать **попытку
создания** probe directory/files через filesystem seam/наблюдение, а не
только сравнивать directory listing после cleanup. Тест на read-only source
не должен зависеть от того, запущен ли процесс privileged.
**Проверка reviewer:** весь create/edit/revalidation source путь read-only;
проверка пересечения не потеряна и нет write probe в общем предке source.

### 7. Ввести согласованный persistence snapshot инспектора (A08)

**Исполнитель:** implementer; quick может обновить интерфейсные fixtures.
**Зависимости:** нет. **Область:**
`backend/internal/service/source_location_detail.go`,
`backend/internal/persistence/source_analysis.go`, `source_analysis_active.go`,
новый persistence detail reader и inspector API tests.

Добавить один persistence-метод, возвращающий root/location/optional variant/
active operation в read-only `REPEATABLE READ` transaction. Все запросы
используют один `tx`, а не `repository.db` вне него. Не брать write locks
для обычного GET. Service получает один snapshot и по-прежнему парсит
технический JSON/строит DTO; Bun/SQL не переносятся в service/API.
Сохранить существующие root-not-found/location-not-found ответы.

Не маскировать нарушение связи «variant ID есть, result отсутствует»
искусственным `not_analyzed` или 404: согласованный snapshot устраняет
гонку удаления variant, а прочие ошибки БД/повреждённого JSON остаются
настоящими ошибками. В валидном snapshot ID и result присутствуют вместе.

**Проверка tester:** используя PostgreSQL query barriers, остановить read
после location при уже существующей running analysis; выполнить первый
atomic analysis apply. Ответ — старый
snapshot без result, **с** прежним active operation, либо новый snapshot
с result и terminal operation вне active; не смесь nil/nil из разных
моментов. Второй тест: location связан с A, operation holds на A отсутствуют;
параллельно unlink+orphan cleanup действительно удаляет A. Reader возвращает
согласованный старый result либо согласованное новое состояние, не 500.
Отдельно проверить удаление root/location и foreign location (404), repeat
analysis с предыдущим result и активной operation; GET не читает filesystem.
**Проверка reviewer:** все четыре сущности в одном snapshot, ни одного
незащищённого дочитывания после transaction; UI/generated контракт прежний.

### 8. Зафиксировать и проверить платформенный контракт безопасного source I/O (A06/A09)

**Исполнитель:** implementer. **Зависимости:** нет; обязательный prerequisite 9–11.
**Область:** source filesystem adapter, `backend/internal/integrations/tools/`,
platform tests и `.github/workflows/ci.yml` при необходимости.

До переключения scan/analysis реализовать и проверить узкую абстракцию:
закреплённый root directory, no-follow открытие каждого компонента,
чтение directory entries через открытый directory handle, открытый regular
file с metadata и seekable транспортом в ffprobe. Relative path не может
содержать absolute/traversal; directory/file symlinks и Windows reparse
escapes запрещены. Простые `Lstat`/`EvalSymlinks` до/после pathname открытия
не являются этой защитой. `os.Root` без доказанного no-symlink контракта
также недостаточен: containment не равен запрету внутренних symlinks.

Закреплять root также безопасно: однократный обычный `os.Open` всего
абсолютного пути оставляет окно подмены его предков. Открывать нормализованный
root покомпонентно от filesystem/volume anchor. Доверенную mount topology
и ограничения pinned-directory semantics при rename описать явно; не
обещать filesystem transaction или immutable bytes. На Windows отвергать
junction/reparse points и опасные namespace/ADS/device формы, а не только
`ModeSymlink`. На Unix не трактовать backslash в имени как separator.
Terminal open не должен зависнуть на подменённом FIFO до проверки regular
file; нужные flags входят в платформенную приёмку.

Platform implementations вынести в build-tagged adapters. Для Unix
рассмотреть handle-relative `openat`/`O_NOFOLLOW` на каждом компоненте;
для Windows проверить handle-relative/no-reparse эквивалент на реальной CI.
ffprobe получает закреплённый открытый объект, а не исходный pathname.
Подтвердить seek support выбранного FD/handle транспорта на одобренных
managed builds; `pipe:0` не считать полноценной заменой seekable file.

Конкретный первый кандидат транспорта: FFmpeg `fd` protocol
(`-fd N -i fd:`); Unix — `exec.Cmd.ExtraFiles` и child fd 3, Windows —
открытый regular file как `Cmd.Stdin` и child fd 0. `ExtraFiles` на Windows
не поддерживается. Windows fd 0/CRT seek и наличие `fd` protocol во всех
approved binaries **нужно доказать**, а не предположить. Родитель не читает
тот же descriptor одновременно с ffprobe (dup разделяет offset).
Референсы для реализации:
<https://pkg.go.dev/os#Root>, <https://pkg.go.dev/os/exec#Cmd>,
<https://ffmpeg.org/ffmpeg-protocols.html#fd>,
<https://man7.org/linux/man-pages/man2/openat.2.html>.

**Разбиение шага 8 на отдельные change sets:**

1. **8a — контракт и seams.** Технический пакет, например
   `backend/internal/integrations/sourcefs/`, без DB imports: pinned root,
   directory enumeration, safe regular-file open, stat, ownership/Close.
   Сначала зафиксировать гарантии/ограничения и тестовый barrier contract;
   production callers пока не переключать. Проверка tester/reviewer —
   traversal/path rejection, cleanup и отсутствие небезопасных defaults.
2. **8b — Unix adapter.** Linux: кандидат `openat2` с
   `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS`; проверить kernel/seccomp и
   закреплённый `x/sys`. macOS: single-component `openat` от parent fd с
   no-follow/directory flags. Fallback допустим только после отдельного
   доказательства его guarantees, не обычный pathname open. Проверка —
   native Linux/macOS CI, swaps, FIFO/cancel, directory enumeration и Close.
3. **8c — Windows adapter.** Кандидат handle-relative `NtCreateFile`, один
   component, open-existing/no-reparse flags, проверка полученного handle
   до использования; подтвердить query/share/enumeration API из имеющегося
   `x/sys/windows`. Проверка — Windows amd64 CI для junction/file reparse,
   ancestor swap, share conflicts и cleanup; unsupported FS fail closed.
4. **8d — transport capability и evidence.** Узкий starter/fixture для
   inherited regular file и `fd` protocol; доказать seek на approved
   builds всех платформ. Добавить CI coverage, не глобально обнулять тест
   через `Skip` при отсутствии protocol. Capability failure до traversal
   классифицировать как tool failure, не root unavailable и не массовый
   `probe_error` файлов. Это prerequisite production wiring шага 9.

8b и 8c можно выполнять независимо после 8a; transport evidence 8d —
параллельно с adapters после фиксации контракта. Общий шаг 8 закрыт только
после приёмки всех четырёх change sets.

**Готово, когда:** tester предъявил regression с подменой ancestor/file
между проверкой и открытием, успешное чтение fixture с требуемым seek и
закрытие handles при success/error/cancellation. Reviewer принял механизм
для каждой поддерживаемой ОС. Платформенные runs — в CI, не локальная
кросс-сборка. Неподдерживаемый транспорт обязан fail closed без fallback на
pathname; это blocker завершения плана на заявленной платформе, а не
разрешение навсегда отключить там scan/analysis. Если контракт нельзя
реализовать без изменения in-place дизайна, остановить 9–11 и вынести
конкретный вопрос владельцу.

### 9. Подключить закреплённый seekable input к обоим ffprobe adapters (A06/A09)

**Исполнитель:** implementer. **Зависимости:** 8.
**Область:** `backend/internal/integrations/tools/ffprobe.go`,
`ffprobe_analysis.go`, runner interfaces/tests и соответствующие worker factories.

Заменить unsafe pathname input для source probes на контракт открытого
источника шага 8. Сохранить allowlisted executable, pinned installation,
лимиты stdout/stderr, timeout/cancellation, запрет сетевых/protocol escapes
и текущие ffprobe queries. Учесть вторичные открытия demuxer-ом: вход не
должен давать возможность читать произвольные связанные файлы/URLs.
Разделить пользовательский relative path и transport name; внутренний
`fd`/handle путь не должен стать путём файла в inventory/UI или утечь в
safe errors. `format.filename` в raw ffprobe JSON может стать `fd:`:
сохранять raw JSON неизменным, не подменять filename для красоты. Проверить
отсутствие зависимости parser/UI от старого raw pathname. Не оставлять
публичный обход через старый source pathname API.

**Проверка tester:** оба adapters действительно читают pinned bytes,
включая transient swap перед фактическим чтением; seek-dependent fixture
даёт те же format/streams/tags, что обычный валидный анализ. Проверить
bounded output, cancel/timeout, child exit и handle cleanup; negative fixture
со вторичным внешним resource не читается.
**Проверка reviewer:** не появился unsafe fallback, network/staged mode,
новый download source или изменение lifecycle managed tools.

### 10. Перевести scan traversal и confirm на root-scoped I/O (A06)

**Исполнитель:** implementer. **Зависимости:** 8–9.
**Область:** `backend/internal/service/source_walk.go`, `source_scan.go`,
scan worker wiring и `source_walk`/`source_scan` tests.

Walker использует pinned directory handles для recursion и stat; probe
читает открытый файл из того же безопасного root. Не возвращаться к
`ReadDir/Lstat/ffprobe` по небезопасному absolute pathname в confirm.
Сохранить exact relative path, 13 extensions, bounded batches, reuse
unchanged statuses и правила cleanup candidates при stale/cancel/failure.
Не держать открытым handle каждого файла всего root до конца scan:
ограничить descriptor lifetime и безопасно переоткрывать через root adapter.
Повторное подтверждение namespace/metadata не заменяет безопасное открытие.

**Проверка tester:** barrier после directory entry перед recursion/probe;
заменить parent symlink-ом на внешний каталог с marker media. Внешний
каталог/файл не читается и не попадает в candidates, даже при совпадающих
size/mtime. Проверить file symlink, внутренний directory symlink и возврат
оригинала до confirm; valid nested root и unchanged reuse работают.
Failure сохраняет прошлый published inventory и очищает partial candidates;
утечки handles отсутствуют.
**Проверка reviewer:** закрыты recursion, probe и final confirm, а не
только одна точка; прежняя классификация root unavailable не испорчена.

### 11. Перевести technical analysis на закреплённый file object (A09)

**Исполнитель:** implementer. **Зависимости:** 8–9.
**Область:** `backend/internal/service/source_analysis.go`,
`source_analysis_path.go`, analysis worker и stale/analysis tests.

Безопасно открыть source из immutable operation snapshot относительно
закреплённого root, сверить regular-file stat с snapshot до чтения и после
чтения **того же объекта**, отдельно безопасно подтвердить актуальность
namespace. Передать этот объект adapter-у шага 9. Сохранить точность stat,
DB fencing при apply, read holds, idempotence/retry/recovery и сохранение
прошлого result при ошибке. Возврат оригинального pathname до post-check
не должен позволять опубликовать технические данные внешнего объекта.

**Проверка tester:** в точке фактического probe-open временно подменить
file/ancestor symlink-ом наружу, вернуть оригинал до post-check. Сравнить
известные различающиеся tags/streams оригинала и внешнего fixture: внешний
объект не прочитан; допустим анализ pinned original или безопасный stale
отказ, но не публикация внешних данных. Существующий тест persistent swap
оставить. Проверить stat change, namespace replacement, cancel, apply
conflict, duplicate delivery и отсутствие variant mutation после failure.
**Проверка reviewer:** защита открытия не сводится к pre/post Lstat или
сравнению inode после уже совершённого чтения; stat-модель не объявлена digest.

### 12. Пройти общий сценарий и независимую приёмку

**Исполнитель:** tester + reviewer независимо.
**Зависимости:** 1–11. **Область:** evidence и точечные эксплуатационные docs.

На PostgreSQL и реальных approved managed ffprobe проверить обычный путь:
Setup → tools download/verification → move → удаление неактивной installation;
source create → scan → manual analysis → inspector/reload → repeat analysis →
source delete без изменения source bytes. Проверить GET/Complete из двух
вкладок и сохранение предыдущего result после failure. Обновлять UI только
если для исправления действительно нужно; SSE остаётся wake-up для REST,
не источником detail payload.

Запустить `task verify`; reviewer независимо сопоставляет все A01–A09 с
изменениями/tests и повторяет gate. Для платформенного I/O приложить CI
evidence Linux/macOS обоих arch и Windows amd64, включая реальный managed
ffprobe seek/read, не только компиляцию. Отсутствующую CI/manual проверку
явно назвать blocker/непроверенным условием, не выдать за PASS.

Передача: revision, diff, этот план, имена regression tests и управляемые
interleavings, gate exit/log, CI evidence, результаты реального сценария,
оставшиеся ограничения. Вердикт — COMPLETE либо конкретные blockers.
В `done/` переносить только после COMPLETE; один PASS старых tests не
доказывает исправления непокрытой гонки.

## Критерии готовности

| Критерий | Доказательство |
| --- | --- |
| Delete использует текущий защищённый tools root и exact files. | Шаг 1: move-before-delete barrier, files + DB assertions. |
| Прямая смена root не конкурирует успешно с install для старого root. | Шаг 2: оба порядка commit, worker до/после root read, atomic settings. |
| Собственные probes не дают ложный отказ Setup. | Шаг 3: State/Complete/SaveRuntime/ValidatePaths, общий фактический probe directory; completion/empty условия и отказ чужому содержимому сохранены. |
| Rollback inventory проходит при истории scan. | Шаг 4: production-shaped rows, migrator chain, повторный up. |
| Устаревшее path/count подтверждение не удаляет root. | Шаг 5: PATCH/scan-before-delete barriers, conflict без потери данных. |
| Source validation не пытается писать в source. | Шаг 6: наблюдение create attempts, cases/aliases/readonly tests. |
| Detail представляет единый snapshot без race-induced 500. | Шаг 7: first apply и unlink/orphan cleanup concurrent reads. |
| Scan/analysis не читают внешний объект через symlink replacement. | Шаги 8–11: no-follow adapters, pinned probe input, transient ancestor/file swaps. |
| Сохранены seek, лимиты, отмена и поддерживаемые платформы. | Шаги 8–9, 12: real managed ffprobe fixtures + platform CI evidence. |
| Gate и независимая приёмка пройдены без ослабления контрактов. | Шаг 12: `task verify` exit 0, COMPLETE, перечисленные ограничения. |

## Итог выполнения и доказательства (2026-10-04)

| Шаг | Реализация | Приёмка / результат |
| --- | --- | --- |
| 1 / A01 | `5c0fb8b` | PASS; независимая матрица A01, persistence/service regression coverage. |
| 2 / A02 | `ba37b3b`, `f6134c9` | PASS; независимая матрица A02, transactional root/enqueue race coverage. |
| 3 / A03 | `7eba315` | PASS; probe guard, в том числе отсутствующий output с общим существующим предком. |
| 4 / A04 | `2519ab9` | PASS; миграционный rollback с историей scan. |
| 5 / A05 | `2b40ef0` | PASS; path/count confirmation проверяется под lock. |
| 6 / A07 | `6eef618` | PASS; source overlap validation read-only. |
| 7 / A08 | `3a7a365` | PASS; согласованный persistence snapshot и race regressions. |
| 8 / A06/A09 | `af250da`, `1059997`, `79fb8ef`, `3ab1f57` | PASS; Unix/Windows sourcefs adapters и managed transport. |
| 9 / A06/A09 | `d35a2bd` (после перечисленных step-8 fixes) | PASS; ffprobe читает pinned seekable object. |
| 10 / A06 | `12963b8` | PASS; scan traversal/confirm через root-scoped I/O. |
| 11 / A09 | `1ca975c` | PASS; analysis использует pinned source object. |
| Исправления и CI | `2784703`, `cef527c`, `84a0801`, `2fb0cb1`, `7d31194` | Ранние CI failures сохранены исторически; см. CI chronology в evidence report. |
| 12 / independent acceptance | `acad608`, `a42f09f` | PASS; independent `task verify` exit 0; native CI `37153819171` all jobs SUCCESS; реальная macOS acceptance пройдена и cleanup подтверждён. |

Независимый gate использовал Go test cache там, где это применимо. Windows
UNC/SMB не поддержан в согласованном scope; попытка Windows ancestor namespace
swap завершилась `ERROR_ACCESS_DENIED`, поэтому её нельзя описывать как
успешный swap. Ручной `/api/setup/paths/check` не включал candidate path keys и
проверил только сохранённые пути; это не выдаётся за ручную проверку пустых или
явно заданных кандидатных путей. Остальные ограничения и ссылки на полные
доказательства см. в `docs/reports/plan06-independent-review-2026-10-04.md`.
