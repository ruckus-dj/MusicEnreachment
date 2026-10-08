# План 09 — предложенный контракт staged source analysis (D03–D05)

**Дата:** 2026-10-08. **Статус:** техническая проработка предложения; не
разрешение на реализацию. Независимое ревью проведено 2026-10-08
([отчёт](../../reports/plan09-independent-review-2026-10-08.md)); explicit owner
approval ещё не получен, документ остаётся предложением. Требования владельца
зафиксированы в
[решениях](09-staged-source-analysis-owner-decisions.md). Фактическое текущее
исполнение описано в [D01](../../reports/plan09-execution-map-2026-10-08.md).
Сохранённые ниже имена полей, endpoints, SQL layout и физические пути —
предложения для review, не новые продуктовые решения. При расхождении с
решениями владельца приоритет имеют решения владельца.

## 1. Предмет и известные границы

- Источник остаётся read-only. Scan только перечисляет root, получает stat
  кандидатов и планирует отдельные per-file processing jobs; scan не копирует,
  не probe-ит и не хеширует файлы.
- Режим (`in_place`/`staged`) выбирается явно для каждого нового root; default
  нет. Смена режима влияет только на будущую работу. Processing concurrency —
  отдельное runtime setting, начальное значение **4** по явному решению владельца;
  это лимит одновременно обрабатываемых файлов, не provider RPS.
- Durable job arguments минимальны: operation ID, необходимые root/location/work
  IDs и явный intent. Остальные рабочие settings/config читаются из текущей БД
  при старте/retry/execution. Полный `input_snapshot` конфигурации не
  сохраняется и не копируется в operation/job. Сохранять только минимальную
  delivery identity/fence, необходимую для надёжности и валидации актуальности.
- Изменившийся size/mtime делает работу устаревшей. Неизменённые файлы
  автоматически повторно не ставятся; повтор ошибки — вручную; дубликаты одной
  работы не создаются. Смена режима влияет только на будущую работу и не
  переписывает уже созданные результаты и завершённые операции: ещё не
  выполненная queued/retry работа читает текущий mode при execution, а не
  сохранённый при admission.
- Audio copy нужна лишь staged mode. Существующая пригодная copy сохраняется для
  retry/restart, пока все запрошенные шаги этой работы не завершились успешно.
  После успеха она становится eligible для явной bulk cleanup. Ошибка cleanup не
  превращает analysis success в failure. Никаких квот, произвольных размеров и
  fallback в in-place.
- Output обязателен в Initial Setup и остаётся non-empty setting: его можно
  изменить, нельзя очистить. Перед reset валидируется новый путь; reset запрещён
  при любых активных задачах; при разрешённом reset queue/tasks и DB references
  старого output атомарно инвалидируются, но старые файлы не удаляются и остаются
  unmanaged. Сохраняются локальные entities, source links, inventory и analyses;
  автоматического republish нет.
- Области output автоматически создаются; одобренные логические названия:
  `analysis`, `publication`, `checks`, `media`. Точные физические имена/layout —
  техническое предложение. Этот документ описывает staging-подготовку анализа;
  атомарная публикация остаётся вне плана 09.

## 2. D03 — UI-flow (предложение)

### Sources: root list и создание

В existing Sources screen списка корней добавить действия «Добавить источник» и
явный выбор режима в форме создания: `In-place` или `Staged`. Ни один radio не
выбран заранее; submit disabled до выбора. Краткое объяснение различает прямое
чтение источника и подготовку audio copy в writable output. Показывается именно
server/container path, существующий source path validator проверяет root.
Редактирование существующего root позволяет изменить mode для будущих jobs,
не вызывает reanalysis и не меняет active operations; до submit виден текст о
границе влияния. Добавление запускает ручной scan.

### Output и concurrency settings

Settings показывает обязательный output path (очистка запрещена), действие
«Проверить новый путь» и отдельное подтверждение «Сменить output». Новое значение
не применяется до успешной проверки и атомарного reset; при queued/running задачах
кнопка disabled с перечнем причины/активности и повторным readback. Draft формы не
подменяет сохранённое значение. Изменение concurrency показывается отдельно,
не связывается с RPS и остаётся DB-backed setting; значение 4 — одобренная
начальная настройка. Интерфейс не представляет число как пользовательскую квоту.

### Per-file operation и inspector

На operation list отображать scan и per-file processing как отдельные jobs.
Operation REST snapshot остаётся источником истины; SSE остаётся только wake-up,
после которого UI повторно читает snapshot. Inspector отображает статус файла,
каждого запрошенного шага, safe error, retry, подготовку/повторное использование
copy, cleanup pending/error, но не обещает progress percentage без backend total.
Retry конкретного шага доступен только для failed шага. Успешный соседний шаг и
его provenance остаются видимыми при ошибке/повторе другого шага.

### Явная очистка

Bulk action «Очистить завершённые staged-копии» показывает число eligible items,
не предлагает выбор недоступных/live artifacts и не подразумевает немедленного
удаления. Ошибки отдельных удалений остаются cleanup failures, копия остаётся
учтённой для следующей попытки. Подтверждение и список результатов доступны
оператору; неуспех cleanup не переписывает успешный analysis.

### Состояния и доступность

Для screens/controls предусмотреть loading, empty, API error, stale output/root,
dirty draft, validating, rejected, queued/running, step failure, partial success,
cleanup eligible/in progress/failure и completed. Ошибка привязана к полю или
операции, доступна текстом, не только цветом. После completion/reconnect перечитать
REST snapshot и сохранить несохранённый draft. Tab/Shift-Tab, visible focus,
keyboard activation, focus restoration после закрытия диалога и screen-reader
labels обязательны для proposal review. Проверить desktop screenshots light/dark;
375px — только проверка сохранности действий, не mobile-first требование.

## 3. D04 — lifecycle prepared audio input

### Предложенный lifecycle для каждой работы

1. **Scan/enumeration.** Scan обходит root, не следует symlinks и перечисляет
   files/stat only. В транзакции фиксирует завершённое наблюдение inventory,
   синхронизирует locations и создаёт не более одной ожидающей работы для нового,
   изменённого или явно ручного retry файла. Недоступная область удаляет только
   locations этой области; нечитаемая location не сохраняется как будто доступна.
   Root/collection/SHA cache остаются. Обход не создаёт audio copy и не запускает
   анализ.
2. **Admission.** Per-file operation получает root/location/work IDs и intent;
   enqueue вместе с DB operation/step fences атомарен через `River.InsertTx`.
   Валидация читает текущий root mode, output path, analysis settings и active
   tools, а не восстанавливает историческую полную конфигурацию. Для staged
   работы перед side effect проверяются актуальность location и доступность
   output. Tools/source/output read/write holds согласуются с root deletion,
   mode/output changes и tools-root operations.
3. **Owned-copy acquisition (staged only).** Worker открывает конкретный source
   через текущий безопасный opener, получает до копии size/mtime, создаёт
   эксклюзивный owned artifact в staging area, последовательно копирует один раз
   в файл и после чтения валидирует size/mtime против наблюдения. При mismatch
   copy/result отбрасываются, analysis не публикуется. In-place путь использует
   открытый source input без copy; режим выбирается per root.
4. **Step execution.** Один пригодный prepared input переиспользуется SHA,
   `ffprobe`, `fpcalc`; source bytes повторно не читаются для запрошенных steps.
   Steps независимы: запись success одного шага не откатывается из-за ошибки
   другого; ручной retry failed step использует retained copy. Retry/restart
   проверяет artifact identity/availability и source size/mtime; если пригодной
   copy нет — снимает stale DB reference и создаёт новую при необходимости.
   Никакого fallback in-place при copy error/ENOSPC.
5. **Apply results.** Перед DB publication worker повторно валидирует source
   identity/size/mtime и fenced delivery ownership. DB transaction применяет
   результат только при совпадающей live fence и актуальном work. Step success
   становится visible в установленной per-step модели. Copy остаётся retained,
   пока каждый запрошенный step работы не имеет requested success.
6. **Terminal/cleanup.** После всех requested steps success запись copy
   переходит в eligible-for-cleanup. Сама файловая очистка запускается только
   явным bulk action. Копия, используемая живой работой или незавершённой
   обработкой, не eligible. Ошибка удаления сохраняет ownership и не меняет
   analysis success. Возможна множественность eligible копий без quota/TTL.

### SHA/cache — решение владельца от 2026-10-08

SHA — независимый запрашиваемый step; hash backfill для неизменённых файлов не
создаётся. На один уникальный SHA существует ровно один последний успешный
fingerprint. Версия `fpcalc` сохраняется как provenance, но не является частью
cache identity и не образует retained history. Если текущий выбранный `fpcalc`
требует пересчёта, прежний результат остаётся текущим/доступным, пока повтор
работает или завершается ошибкой; только успешный новый результат атомарно
заменяет его. Никакой промежуточный failed/running status не затирает последнюю
successful result row. При первом вычислении успешного результата ещё нет. Этот
контракт supersede-ит version-keyed reuse в текущем коде/физической схеме: schema
refactor необходим, и историческая схема не считается уже доставленной. Прежние
успешные fingerprint и успешные sibling steps сохраняются при ошибке probe или
fingerprint rerun; ошибки шагов остаются независимыми. Текущий выбранный инструмент
проверяется при execution/retry, а не фиксируется старой cache identity.

### Artifact ownership, fencing и crash matrix

Предлагаемая запись artifact содержит случайный `artifact_id`, work/location ID,
канонический относительно managed output path, ожидаемые size/mtime/length,
создавшую operation ID + attempt/job ID, lifecycle state, и timestamps. Имена —
proposal. Реестр в БД доказывает ownership; случайное имя само по себе не
доказывает. Создание файла — эксклюзивное, без overwrite. Старый delivery может
удалять/менять только свой artifact при сравнении operation/attempt/job fence;
если есть live fence или неизвестный/foreign owner — отказ без удаления. Нельзя
удалять путь, найденный только сканированием каталога, либо «почти совпавшее»
имя. Проверки path containment и symlink-safe операции остаются обязательны.

| Crash/failure boundary | Durable evidence / restart response | Разрешённое удаление |
| --- | --- | --- |
| До artifact row/file | Operation/step fence; нет artifact | Ничего |
| Artifact row создан, file ещё нет | Owned row в acquiring | Только fenced owner/recovery, если delivery доказанно не live |
| File создан, copy частичная / ENOSPC | Owned row + exact path + delivery fence; пометить incomplete/retryable | Только если fence больше не live и ownership row совпадает; не удалять foreign file |
| Copy готова, DB ready state ещё не записан | Row acquiring + fenced delivery; recovery сверяет файл/identity | Не удалять пока delivery live; orphan удалять только после fenced recovery |
| Copy ready, до/во время tool execution | Row retained + running step delivery; recovery делает retryable, copy сохраняется | Не удалять live/unfinished copy |
| Один tool success, sibling error/crash | Успешный step опубликован отдельно; requested set остаётся незавершённым | Не удалять; retry использует copy если пригодна |
| DB step success committed, process ещё live | DB state/fence authoritative; state transition идемпотентна/fenced | Не удалять до terminal success и explicit cleanup |
| Все requested steps success, до cleanup action | Artifact eligible, DB results success | Только explicit bulk cleanup, после повторной проверки отсутствия live owner |
| Bulk cleanup начат, unlink завершён, DB row ещё есть | Owned row + file absent; retry cleanup снимает row | Повторно удалять только тот же fenced owned artifact; отсутствующий путь — reconcile |
| Unlink error / permission/I/O failure | Keep row, record safe cleanup failure, success unchanged | Не менять ownership и не снимать row |
| Root removed/output reset/config changed during work | Active-task gate/coordination должна запретить конфликт; foreign/stale delivery fails fence | Старые/чужие paths не трогать; reset не удаляет physical files |
| Process restart, delivery live in River | Reconciler не считает job orphan | Ничего; worker продолжает/retries |
| Process restart, delivery orphan | Reconciler fenced-terminalizes work; copy пригодность проверяется при ручном retry | Cleanup только если fence invalidated и artifact ownership подтверждён |

В этой фазе **не обещать same-filesystem atomic rename/publication** и не считать
rename доказательством crash safety. Final promotion/atomic replace не требуется
для analysis input; при необходимости отдельный post-review контракт. Recovery
не удаляет live или foreign artifact.

### Concurrency/resource policy

Runtime setting ограничивает одновременно обрабатываемые файлы значением default
4. Это owner-approved default, не квота на размер/суммарное место и не RPS.
Внутри одной staged file операции — один последовательный copy; несколько
independent files могут идти с заданной concurrency. На ENOSPC шаг подготовки
fail-safe; оператор устраняет причину и делает ручной retry. Не добавлять новый
таймер, auto retry, quota или fallback.

## 4. D05 — DB/API/deployment предложения

### Предлагаемая физическая модель (review, не migration)

- `source_root.processing_mode TEXT NOT NULL`, ограниченный `in_place|staged`.
  Для существующих roots потребуется backfill `in_place`, сохраняющий текущий
  путь. Создание root требует явного значения; UI не посылает default.
- Typed runtime setting `source_file_concurrency`, стартовое значение `4`.
  Обычная settings registry/table validation, без env var.
- Output path остаётся существующей обязательной настройкой. Предложение
  физического layout: `<output>/analysis/staging/<root-id>/<work-id>/<artifact-id>`;
  logical areas `analysis`, `publication`, `checks`, `media` создаются при
  проверке/создании output. Это имена предложены для обсуждения, не утверждённые
  физические имена. Не использовать второй общий work-directory setting.
- Owned artifacts — нормализованная таблица (candidate name
  `source_analysis_artifact`) со ссылками work/location, relative output path,
  expected source identity (size/mtime; SHA если уже запрошен/получен, без
  обязательного backfill), ownership delivery triple, состояния `acquiring`,
  `ready`, `cleanup_eligible`, cleanup error/time. DB reference и operation/step
  fences связаны FK; индексы нужны на active owner и eligible cleanup. Названия,
  columns и enum могут измениться на design review.
- Per-file durable work/step rows переиспользуют существующие normalized
  entities/step model там, где семантика совпадает; операции имеют только
  минимальный payload. Полная копия current settings, tool paths, selected tool
  snapshot, output config и source file bytes в `operation.input_snapshot` не
  добавляются. Для безопасности immutable identity (root/location/work ID,
  explicit retry intent) и attempt/job fence не являются copied config.
- Root mode updates блокируются/сериализуются с admission на root; только новые
  работы видят новое значение. Удаление root остаётся запрещённым при активных
  задачах и удаляет очередь/locations; physical source/audio bytes не удаляет.

Миграционный путь: это unpublished prototype; совместимость с историческими
опубликованными данными не требуется. Squash миграций разрешён, но не обязателен;
точную стратегию выбрать в implementation review. Не представлять legacy
backfill/upgrade path как продуктовое требование и не добавлять destructive down
без явного описания его поведения.

### Operation admission и output reset

Оставить внешний job args скудными и транзакционными: River args — operation ID,
текущая `Operations`/reconciler схема; operation rows содержат необходимый target
и explicit intent. Admission в одной DB транзакции: lock coordination gates в
детерминированном порядке → проверить root/output setting и отсутствие конфликтной
мутации → создать operation + per-file references/fences → `River.InsertTx` →
commit. Rollback оставляет ни operation, ни job, ни artifact claim. Worker
не восстанавливает исторический mode: queued и retry работа читает текущий mode
root из актуальной БД при выполнении и retry (решение владельца: «остальные
входы перечитываются из актуальной БД при выполнении и retry»). Admission не
сохраняет admitted mode; уже завершённые операции и записанные результаты при
смене mode не переписываются.

Output reset требует двухфазного пользовательского flow, но не двухфазного
применения: сначала validate-only кандидата path; затем exclusive global
output/admission gate. Все operation admissions и queued-to-running worker claims
берут тот же cross-process gate shared, в едином порядке до reset-token/root/
operation/domain rows. Reset под exclusive gate проверяет отсутствие незавершённого
reset token, перечитывает setting и active operations; любая queued/running задача
запрещает reset. Gate удерживается через filesystem steps и DB commit, поэтому
между проверкой и инвалидацией не может проскочить admission/claim. При crash lock
освобождается, но durable `preparing` token заставляет admission/claim отказывать
до startup recovery. Предложенный cross-process primitive — PostgreSQL advisory
lock; точный вариант подтвердить при implementation review. В одной DB transaction
обновить setting и инвалидировать все queue/tasks и все DB refs старого output;
не ограничиваться staging/publication refs. Старые физические файлы не удалять.

Filesystem mkdir не атомарен с DB commit. Предлагаемый recoverable journal:
создать reset token в `preparing` после захвата gate; до DB transaction создавать
только отсутствующие logical directories эксклюзивно и сохранять точный список
каталогов, созданных этим token (не присваивать существующие). Одна DB transaction
меняет setting, инвалидирует все queue/tasks и старые output refs и помечает token
`committed`. После commit token finalized. При обычном rollback удалять лишь
записанные token-owned каталоги, если они всё ещё пусты, затем закрыть token.
При startup recovery `preparing` без commit marker означает rollback: сохранить
старое setting/refs и удалить только доказанно owned пустые dirs; `committed`
означает finalize без удаления paths. Если ownership или пустота не доказаны,
оставить каталог и предъявить recovery error оператору. Старые физические файлы
не трогать. При reset сохранить
local artists/releases/tracks/metadata, source links, inventory/analyses; очистить
только output-owned publication/staging references и связанные queue/task state.
Scope «очистить очередь/задачи» уже одобрен владельцем как **все** queue/tasks и
все ссылки БД на старый output (публикации и staging); этот продуктовый scope не
переоткрывается. Технически остаётся определить invalidation rows и их конфликт
с job history/reconciler.

### HTTP/API proposal

Предпочесть расширение текущих DTO, а не новые endpoints:

- `SourceRootResponse` и create/update request включают `processing_mode`; server
  валидирует только поддерживаемые enum values и не default-ит create.
- Settings response/update включает output directory и `source_file_concurrency`;
  path validation endpoint переиспользует существующий `CheckSetupPaths` pattern
  или typed settings path-check. Output reset может требовать отдельный explicit
  `reset_output` command, если текущий update endpoint не выражает подтверждение.
- Per-file operations используются существующим `GET /operations`,
  `GET /operations/{id}`, retry endpoints. Operation DTO называет targets/status,
  не копирует full config. Новые URL/error codes — открытые технические варианты,
  определить при изучении текущих endpoints и review.
- Bulk cleanup: кандидат `POST /source-analysis/artifacts/cleanup` с eligible
  count/result operation; точная форма endpoint и sync/async граница — review
  issue. Не выдавать cleanup selection за user-facing file picker.
- Ошибки валидируют readable output, path overlap/case-normalization, disk
  write/ENOSPC и stale source; возвращают безопасное actionable сообщение без
  server-local secret/path leaks сверх уже существующего UI server paths.
- Huma contract → экспорт OpenAPI → `frontend/openapi.json` → Orval generated
  client. Не менять `frontend/src/api/generated/**` вручную.

### Deployment

Применить существующий Compose output bind mount как writable managed area:

```yaml
volumes:
  - tools-data:/var/lib/melotrove/tools
  - ./music:/var/lib/melotrove/output
  - /srv/music/sources:/srv/music/sources:ro
```

Явно оставить managed output writable/persistent; source read-only; не добавлять
отдельный scratch bind mount, container overlay, каталог binary/temp или новую env
var. Host path в примере — буквальный существующий default `./music`, а не новая
env var. Standalone deployment обеспечивает, чтобы настроенный абсолютный output
path существовал/был writable внутри процесса. Compose/readme описывают host path
только как mount source, runtime использует container path.

## 5. Оставшиеся технические review details

Точные physical directory/table/column/state/API names, migration strategy
(squash разрешён, не обязателен), cleanup operation response shape и реализация
reset journal выбираются при technical review до соответствующих implementation
commits. Это не новые продуктовые блокеры. Reset scope **все** queue/tasks и все
старые output DB refs фиксирован; cache policy решена владельцем (один последний
успешный результат на SHA, fpcalc version — provenance). Если review обнаружит
реальное противоречие, его нужно вернуть владельцу; не объявлять proposal полностью
approved заранее.

## 6. D02 — сценарная матрица и техническая сверка

Матрица завершает сценарную проработку D02; она не является финальным owner
acceptance технического предложения. Root «disabled» отсутствует. Work-path в отдельных
сценариях означает выбранный writable output и его `analysis` area: отдельная
work-directory/path setting не вводится. Режимы, root lifecycle, cache и output
reset scope следуют решениям владельца; locks, ownership records и recovery ниже —
технические предложения, а не описание текущего кода.

| Сценарий | Ожидаемый результат | Сохраняемое состояние / действие |
| --- | --- | --- |
| Новый SSD root / in-place | Mode обязательно выбран; scan enumerate/stat-only и создаёт per-file jobs; tools читают source | Read-only source, ручной scan; нет default и scan-time analysis |
| Новый HDD/NAS root / staged | То же явное создание; один последовательный audio copy на file; запрошенные шаги используют copy | Без второго path setting, квот и in-place fallback |
| Существующий root, смена mode | Будущая работа читает текущий mode при execution/retry; уже запущенная завершает выбранный путь | Не переписывать старые операции/результаты; mode не сериализуется в job |
| Root/source недоступен при scan | Нет analysis для невидимых файлов; locations недоступной области удаляются по owner rule, root/collections/SHA analyses остаются | Показать server path/actionable error; восстановить диск и вручную scan; disable недоступен |
| Source исчезает или unreadable между stat и open/copy | Delivery fail/stale, incomplete result не применяется; успешный sibling сохраняется | Rediscovery/ручной retry; source bytes не изменять |
| Source size/mtime меняется при copy или перед apply | Отбросить copy/result и fenced apply; не заменять прежний analysis | Retry вручную после исправления; текущесть — owner-approved size/mtime, не stronger threat model |
| Невалидный/недоступный/non-writable output | Validate отклоняет до setting/reset mutation; staged copy не начинается/падает без fallback | Сохранить старое setting/refs и dirty draft; actionable inline error |
| ENOSPC, short read/write, partial copy | Copy не становится ready и не передаётся tools; preparation failure отдельно от tool failure | Предыдущие step successes остаются; восстановить место и вручную retry; без quota/limit |
| ffprobe/fpcalc частичный успех, multi/no audio | Независимые step results сохраняются; fingerprint может быть успешен без matching eligibility | Retry только failed step; matching gate не затирает provenance/success |
| SHA setting on/off; digest hit/miss | Off пропускает SHA, не tools; on только новые/изменённые/explicit retry; известный digest участвует в lookup | Нет backfill неизменённых файлов; toggle сохраняет digest/analyses |
| Fingerprint SHA hit / сменился selected fpcalc | Одна cache запись на SHA; reuse при актуальном результате, иначе вычислить current selected tool | Running/failed rerun оставляет прежний success; replace только по success; нет version history/backfill |
| Manual retry одного шага; partial success | Duplicates suppressed; retry reuses valid retained copy либо пересоздаёт отсутствующую | Не запускать/перезаписывать успешных siblings; нет timer auto retry |
| Active tools/settings сменились при queued/running/retry | Worker читает актуальные БД settings/selected tool; operation args не содержат полный конфиг | Retry использует current selection; live process безопасно завершает текущую работу, provenance сохраняет факт инструмента |
| Restart: row/mkdir/partial copy/ready/tools/step commit | Startup recovery проверяет DB ownership row, exact path и delivery fence; живой River delivery не orphan-ится | Пригодная copy retained; отсутствующая/stale ссылка снимается; cleanup только после fence invalidation |
| Crash после шагов success, до terminal/cleanup | DB result и fence authoritative; copy сохраняется до success всех requested steps | Явная cleanup позже; cleanup error не превращает analysis в failure |
| Bulk cleanup: missing/permission/live/foreign artifact | Только зарегистрированные eligible rows, item-by-item fenced unlink; missing reconcile, I/O error сохраняет ownership | Не сканировать директорию для выбора; повторить «Очистить» после исправления |
| Output reset, queued/running task или гонка worker claim | Любая active task запрещает reset; admission и queued-to-running claim берут shared cross-process output gate, reset — exclusive gate в одном lock order; durable preparing token блокирует новую admission/claim после crash до recovery | Никакой частичной инвалидации при отказе; queued job тоже active; admission не может проскочить между проверкой и commit |
| Output reset: path valid, active tasks отсутствуют | Validate-only → exclusive gate → reread setting/active tasks → одна DB tx меняет setting и инвалидирует ВСЕ queue/tasks и ВСЕ DB refs старого output | Сохраняются local entities/source links/inventory/analyses; старые physical files остаются unmanaged; no republish |
| Reset mkdir/DB error/crash между filesystem и commit | Durable reset token (`preparing`, exact owned-dir manifest, `committed` marker). Создать эксклюзивно только отсутствующие dirs; DB tx меняет setting, invalidates all queue/tasks+old refs и помечает committed. Rollback/recovery удаляет только manifest dirs доказанно пустые; committed token только finalized | До commit старое setting/refs живы; никогда не удалять старые/foreign files. При сомнительном ownership оставить dirs и показать recovery error; нет distributed transaction |
| Root removal | Только без active root operations; удалить queued root work/locations | Source bytes/root analyses/collections остаются; disable не предлагать |

### Текстовое сопоставление D03 с доступными mock screenshots (не visual/browser review)

В каталоге реально присутствуют `sources-1440.png`, `settings-1280.png`,
`sources-375.png`, `settings-375.png` (также desktop Sources/Settings по 1280/1440
и промежуточные размеры). Имена и список файлов проверены; screenshot pixels здесь
не проходили самостоятельный visual review. Из макетов можно только сформулировать
текстовую точку сравнения: Sources содержит root cards с путём/status/actions,
Settings — группы инструментов; узкий layout сводит контент в одну колонку. Эти
экранные состояния не показывают staged mode/output reset/concurrency. В glob
каталога нет Sources/Settings dark screenshots; есть только unrelated
`v2-match-dark-draft.png` и `v2-release-dark-draft.png`. Это не доказывает
непроверенность иных внешних артефактов, но полный dark visual review не заявляется.
Будущий UI review должен проверить canvas/ink/status tokens и статус текстом, не
только цветом, по фактическим light/dark screenshots.

Текстовый mock flow: Sources → Add root → выбрать один незаполненный mode radio
(submit disabled без выбора) → server path → save и manual scan → per-file
operation/status → partial success, retry only failed step → all-success cleanup
eligible → Settings: validate new output → queued/running task блокирует reset →
подтвердить reset после settlement → readback, при ошибке сохранить draft → явно
запустить bulk cleanup. На 375px flow предполагает вертикальные кнопки, переносы
строк, inline errors и focus restoration/keyboard controls; доступность этих
элементов в UI не проверялась. Это textual mock flow, не визуальная оценка screenshot
pixels, не browser run, не новый screenshot и не claim implemented. Финальный UI
acceptance остаётся отдельным этапом.
