# План 07: автоматический поэтапный анализ source-файлов

## Статус и основания

**Статус: согласованный план следующего этапа; не реализован.** План подготовлен
2026-10-05 по решению владельца после завершения ручного source analysis в
`docs/plans/done/05-source-technical-analysis-and-inspector.md` и corrections в
`06-done-plans-audit-corrections.md`. Реализация начинается только по этому
плану; он не является отчётом или доказательством поставки.

Текущая система: оператор вручную запускает scan; scan сохраняет inventory и
`audio/no_audio/probe_error`; для одного `audio` location оператор отдельно
запускает durable `ffprobe` analysis. Нет persisted SHA-256/Chromaprint и
автоматической постановки анализа после scan. Исторические планы 05/06 остаются
неизменными свидетельствами их scope на момент завершения.

## Пользовательский результат и утверждённые границы

### Уточнения владельца при начале реализации (2026-10-05)

Эти явно согласованные в сессии реализации уточнения имеют приоритет над
описанием прежнего quick-probe/manual-analysis разделения ниже:

- Уточнение владельца: это dev-ветка, установок и релизов нет. Поддержка прежней
  модели как «legacy», backward compatibility и параллельные исполнители
  snapshot v1/v2 не требуются. Реализуется одна актуальная модель; требования
  к сохранению успешных шагов, retry/recovery и provenance остаются в силе.

- SHA-256 toggle по умолчанию включён. При включённом toggle hash обязателен
  для всех новых/изменённых файлов инвентаря, включая `no_audio`; digest позволяет
  переиспользовать известный анализ, в том числе результат отсутствия audio.
  Выключенный toggle и запрет backfill неизменённых файлов остаются в силе.
- Выполнять один полноценный `ffprobe`, сразу получающий все нужные данные,
  вместо quick probe при scan и второго technical probe после scan.
  Его результат публикуется только вместе с полностью успешным scan;
  актуальный сохранённый результат не требует второго запуска. Изменение stat
  и повтор ошибочного ffprobe допускают новый запуск.
- Перепроверка `probe_error` при scan остаётся; успешный переход в `audio`
  при прежних size/mtime также инициирует первый автоматический анализ.
- `fpcalc` запускается без явного параметра длины и использует встроенную
  длительность по умолчанию — первые 120 секунд. Приложение не добавляет
  отдельный лимит, но fingerprint не заявляется как рассчитанный по полному файлу.
- Прежний orphan variant сохраняется как кэш только при наличии SHA-256;
  digest-less variant очищается после освобождения references/read holds.
  История прежних результатов у изменённой location не вводится.

Порядок подготовки утверждён: SHA вычисляется (если включён), затем digest
сначала используется для lookup сохранённого успешного `ffprobe`; при cache miss
недостающие `ffprobe` и `fpcalc` могут выполняться параллельно. `fpcalc` может
закончить до подтверждения audio eligibility: его успешный результат сохраняется
при ошибке `ffprobe` и остаётся доступен для reuse по `(SHA-256, fpcalc version)`
независимо от probe; single-audio gate ограничивает только matching, поэтому
fingerprint не участвует в matching, пока успешный `ffprobe` не подтвердит ровно
один audio stream. Пункт 1 проектирует физическое
представление и исполнение этого порядка, не меняя его. Это решения владельца, а
не доказательство реализации.

Оператор по-прежнему явно запускает scan. После **полностью успешно
завершившегося** scan новые и изменённые locations автоматически получают
поэтапный анализ: сохранение технического `ffprobe`, опциональный SHA-256 и
Chromaprint через managed `fpcalc`. Отдельно нажимать Analyze на каждом файле не
нужно. Глобальная настройка SHA-256 вкл/выкл — типизированная runtime setting в
PostgreSQL с управлением через Settings UI; новой env-переменной нет. Никакого
scheduled scan этот этап не добавляет.
SHA-setting фиксируется при создании scan operation в её durable
snapshot, поскольку hash preparation выполняется до probe. Последующий analysis
work наследует эту policy, не читая toggle заново; explicit retry failed hash
сохраняет исходную policy. Переключение не меняет уже поставленную работу и не
создаёт hash intent для неизменённых locations без digest.
Само переключение не создаёт analysis jobs и не инициирует массовый rehash.

- Location текущий, если наблюдаемые `size` и `mtime` совпадают. При включённой
  SHA-настройке hash выполняется для новых locations и для источников, изменение
  которых по `size`/`mtime` требует нового анализа. Не заполнять hash у старых
  неизменённых locations без digest — ни при включении настройки, ни лениво в
  unrelated обработке. Защищать от замены bytes при прежних size/mtime не требуется.
  Изменённое содержимое считается новым анализом источника. Включение настройки
  не инициирует массовый backfill/rehash.
- При выключенной SHA-настройке hash-step получает нейтральный статус
  skipped/disabled, не failure; `ffprobe` и допустимый `fpcalc` продолжаются без
  SHA. Переключение настройки влияет на последующие анализы, но не удаляет ранее
  сохранённые hashes или результаты. Отсутствующий digest (`NULL`) допустим и
  означает, что hash не вычислялся. Неактуальный сохранённый digest никогда не
  используется для reuse; ранее вычисленный актуальный digest сохраняет право на
  reuse, пока его `size`/`mtime` identity остаётся текущей.
- Каждый шаг имеет независимо сохраняемый успех и свою фактическую версию / её
  происхождение. Ошибка одного шага не отменяет уже успешные результаты других.
  Сохраняются ошибки всех независимых шагов; явный retry адресует только
  конкретный ошибочный шаг и не повторяет успешные siblings. Внутренняя batch
  operation — механизм доставки/исполнения, а не отдельный пользовательский
  aggregate result.
- Используемый для matching fingerprint допустим только для файла, чей успешный
  `ffprobe` подтвердил ровно один audio stream. Чтобы не ждать probe eligibility,
  `fpcalc` может выполняться параллельно с probe; успешный output при ошибке
  `ffprobe` сохраняется и остаётся доступен для reuse по `(SHA-256, fpcalc
  version)` независимо от probe. Single-audio gate ограничивает только matching:
  до single-audio подтверждения fingerprint не участвует в matching, но это не
  ограничивает его хранение или cache reuse. При подтверждённом zero/multi-audio
  он не становится используемым для matching fingerprint, состояние остаётся
  unsupported/skipped; успешный результат не отбрасывается. Смена active `fpcalc` не
  запускает массовый backfill. UI показывает версию
  `fpcalc`, использованную для сохранённого fingerprint, и текущую active
  версию. Кроме повтора ошибочного шага оператор может явно запустить отдельный
  точечный ручной rerun fingerprint существующего успешного результата текущей
  active `fpcalc` — только fingerprint, не массовый backfill и не повтор всего
  набора шагов. Если такой rerun завершается неудачей, прежний успешный
  fingerprint и его provenance сохраняются. Уже сматченные/опубликованные tracks
  обычно не требуют повтора только из-за смены инструмента.
- Новый source location с тем же актуальным вычисленным SHA-256 присоединяется к
  той же shared media identity. При совпадении актуального SHA-256 и `fpcalc`
  version fingerprint повторно не вычисляется. Shared identity/dedup и reuse
  возможны только при актуальном вычисленном hash; без hash locations остаются
  независимыми, а fingerprint не является identity. Hash задаёт точное равенство
  bytes, но не уникальность fingerprint или произведения.
- Частичный результат не считается успешным выполнением всех шагов. Не
  придумывать SHA/fingerprint или дополнять snapshot текущими settings.
  Прежний ручной ffprobe-only путь заменяется актуальным поэтапным анализом,
  а не сохраняется как отдельный совместимый исполнитель.
- Вне scope: scheduled scan, automatic retry/backoff, кнопка полного повторного
  анализа как основное действие, настройки/пороговые условия, не утверждённые
  владельцем (кроме согласованного SHA-256 toggle), staged/work directory, inbox groups, matching, AcoustID,
  MusicBrainz, confidence, локальная медиатека и публикация.

Не менять правила `no_audio`/`probe_error` без отдельного ответа владельца.
Multi-stream больше не открытый продуктовый blocker: fingerprint по согласованию
используется для matching только у single-audio-stream файла. Поскольку `fpcalc`
может быть запущен параллельно до получения probe eligibility, его успешный
output сохраняется и доступен для reuse по `(SHA-256, fpcalc version)` независимо
от probe; single-audio gate ограничивает только matching, поэтому output не
участвует в matching без подтверждения ровно одного audio stream и не становится
используемым для matching fingerprint при подтверждённом zero/multi-audio. Сохраняются
допустимые probe и SHA-256 (если hashing включён), показывается нейтральное
состояние unsupported/skipped, а не failure; retry ошибки для него не
предлагается. Технические proposals ниже должны сохранять успешные допустимые
шаги, не трактуя преждевременно полученный fingerprint как допустимый результат. Новое
продуктово видимое состояние/кнопки для ещё не определённых случаев — blocker
соответствующего шага до уточнения.

## Ключевые технические контракты и proposals

### Идемпотентность и хранение результатов

Предложение: представлять ffprobe, SHA-256 и fingerprint как независимые
результаты с собственными версиями/временем/статусом; сохранить полезную
существующую `media_variant` и её ссылки, не перезаписывая исторический успешный
результат неудачным. Повтор шага применяет только его результат; уже успешные
соседние шаги не пересчитываются. При дубликатной доставке worker-а каждый apply
идемпотентен.

Физическую форму — несколько operation kinds, stage jobs одной durable operation
или отдельная нормализованная step state — выбрать после сопоставления с текущей
schema/operations. Обязательный контракт: retry адресует конкретный failed step;
успешный шаг остаётся committed при crash/failure другого; новый scan не создаёт
дублирующую активную работу для той же актуальной location/step.

### Hash identity, variants и provenance

Идентичность определяется только SHA-256: одинаковый digest означает один
`media_variant`, включая locations из разных roots. Variant с digest остаётся
кэшем без locations; orphan variant без digest удаляется после завершения
удерживающих его операций. Для отсутствующего hash сохраняется `NULL`, без
объединения locations. Не выводить SHA identity из tags, размера, mtime,
fingerprint, inode или пути. Переход между существующими данными должен сохранить
FK, references и успешные результаты; конкретная миграция и concurrency —
техническое решение после ревью текущей схемы.

Сохранять provenance реального анализа: факт/версию ffprobe, digest и fingerprint,
алгоритм/версию Chromaprint и фактическую версию fpcalc. Не подменять отсутствующий
или ошибочный результат нулём, пустой строкой либо значением «актуально».

### Автопостановка и восстановление

Обязательный контракт: если scan завершился успешно, автоматическая работа по
его новым/изменённым locations durable и восстановима после рестарта. Не должно
быть окна, в котором inventory успешно зафиксирован, но обязательная работа
потеряна. Предпочтительная техническая proposal — атомарно записать scan
reconciliation и durable enqueue/outbox intent в одной PostgreSQL transaction;
если текущая scan архитектура этому препятствует, предложить эквивалентный
recovery protocol с чётким доказательством отсутствия потерянных locations.
Не ставить analysis после failed/abandoned scan и не менять правила reconciliation.

Recovery повторно выясняет недостающие/ошибочные шаги из durable state и
актуальной location, а не из ephemeral frontend или незафиксированного списка.
Это crash recovery/durable delivery, не automatic retry failed шагов и не
периодическое расписание. Ручной retry проверяет актуальность `(size, mtime)` и
точный target step; stale retry конфликтует и не переписывает старый snapshot.

### Инструменты и ресурсы

Анализ читает source только read-only и применяет freshness contract `(size, mtime)`.
Текущий план реализуется только in-place: локальная копия и staged mode не
реализуются; настройки staged mode сейчас нет. SHA при включённой настройке
сначала даёт digest для cache lookup. Если успешного probe cache hit нет,
недостающие полноценный `ffprobe` и `fpcalc` могут выполняться параллельно.
Fingerprint, полученный до probe eligibility, можно сохранить при ошибке probe;
его успешный результат остаётся доступен для cache reuse по `(SHA-256, fpcalc
version)` независимо от probe. В matching он участвует только при выбранном
успешном актуальном `ffprobe`, подтвердившем ровно один audio stream; при
подтверждённом zero/multi-audio состояние остаётся нейтральным unsupported/skipped
для matching, а успешный fingerprint не отбрасывается. Внутренняя модель private
typed candidate → atomic publish выбранного успешного fingerprint не зависит от
probe success и eligibility; техническому review подлежит конкретная реализация
этой модели, а не допустимость публикации успешного результата по audio
eligibility.
Pin-ить и удерживать от move/delete нужно только managed tools, необходимые
выбранным шагам операции; использовать installation IDs/version snapshots, не
PATH или client path. Activation иной версии после enqueue не должна незаметно
менять выбранный executable. Hash-step и его retry не требуют tool installation.
Fingerprint retry использует уже сохранённый актуальный `ffprobe` result, если он
доступен; повторный `ffprobe` не требуется. Отсутствие инструмента для одного
шага не должно блокировать независимый шаг, которому этот инструмент не нужен.
Переиспользование fingerprint для той же пары `(SHA-256, fpcalc version)` не
требует запуска fpcalc и сохраняет корректную provenance.

Блокировки и read-holds согласовать с существующими scan/tool mutation locks;
два независимых source roots не должны случайно сериализоваться одной глобальной
блокировкой. Применить ту же логику к конкурентным scan, manual retry, tool move/
delete, activation и startup recovery. Не удерживать filesystem/tool locks дольше
необходимого этапа. Конкретный порядок locks — часть design review шага 3.

### UI и ручное действие

Inspector показывает независимое состояние шагов, safe error/retry для
конкретного ошибочного шага, сохранённые фактические версии и active `fpcalc`
version. Явный ручной повтор разрешён: как точечный retry ошибочного шага, так и
отдельный rerun fingerprint существующего успешного результата текущей active
`fpcalc`; смена версии не обязана инициировать его. Не добавлять Analyze для
нормального нового/изменённого файла. Сохранить возможность видеть прежний
успешный результат рядом с поздней ошибкой. SSE — только wake-up, после него UI
перечитывает REST snapshot.

## Порядок реализации

Интеграция шагов последовательна. Независимые адаптеры инструментов и их unit
tests из пункта 4 можно реализовать параллельно пунктам 2–3: они не меняют
persistence, очередь, API или UI. Подключение адаптеров к исполнению и приёмка
пункта 4 остаются после пунктов 2–3. Полные gates выполняются строго по одному.
Typed SHA-setting в registry также можно подготовить независимо: её значение
нужно для immutable scan snapshot в пункте 3. REST/read model и UI этой настройки
остаются в пунктах 6–7; само добавление registry не запускает работу.
Исполнитель сначала читает локальные AGENTS.md, текущие
migrations, models, persistence, service, API, worker, generated client и
соседние tests. Названия новых сущностей ниже — понятия контракта, не требование
к именованию. Конфликт актуального кода с контрактом, необходимость нового
продуктового выбора или нарушения актуального контракта останавливают
зависимый шаг и возвращаются ведущему агенту/владельцу; не исправлять scope
молча.

Локальный build/test gate репозитория — только `task verify`; узкие tests
допустимы только для диагностики его failure. Generated client обновлять штатным
pipeline, не вручную. Коммиты — только по отдельному явному разрешению сессии.

### 1. Сверить физическую модель и спроектировать актуальный state

**Зависимости:** нет. **Область:** `docs/design/data-model.md`,
`docs/design/music_ingest_redesign.dbml`, миграции, operation snapshots,
persistence models.

Составить сопоставление текущих columns/constraints/operation snapshots с
целевыми независимыми шагами, shared SHA identity и lifecycle provenance.
Спроектировать выполнение SHA-first cache lookup, единственного полного probe и
возможного параллельного fpcalc; явно определить private preparation до scan
commit и безопасное сохранение/допуск fingerprint, завершившегося до подтверждения
single-audio eligibility. Не добавлять видимые outcomes или downstream jobs до
полностью успешного traversal.
Предложить физическую схему: какие результаты принадлежат location/media
identity, как сохраняются per-step ошибки и версии, какие уникальные ключи и
FK защищают текущие result references. Проверить migration chain и rollback
актуальной схемы. Не переписывать старые миграции;
не менять концептуальный DBML так, будто предложение уже применено.

Описать единую актуальную форму snapshot: target step(s), tool installations
и stat identity, terminal/recovery states и FK release behavior. Malformed
snapshot получает безопасный отказ с освобождением удержаний; нельзя угадывать
обязательные поля из текущих settings или переписывать сохранённый snapshot.

#### Техническая форма целевой схемы

Ниже фиксируются физические ограничения для реализации утверждённых выше
контрактов; это не дополнительные продуктовые требования. SQL/migrations задают
физическую схему, существующий DBML остаётся концептуальным.

- `media_variant` хранит SHA-256 как `NULL` либо 32 bytes с unique canonical
  identity. Probe result — цельная nullable-группа (`ffprobe_json`, версия,
  policy version, observed tags, время и provenance operation); SHA-only row не
  получает фиктивный probe. Audio count `0` означает успешный zero-audio, `NULL`
  — отсутствие результата. `applied_operation_id` относится к probe, не к
  последнему применившему sibling шагу.
- `media_probe_cache` отделяет cache association от selected probe result:
  PK `(source_sha256, ffprobe_version_sha256, analysis_policy_version)`,
  FK RESTRICT на canonical digest и result. При reuse проверяются размер,
  полный ffprobe banner и policy; digest banner не заменяет сравнение полного
  значения. Первый committed cache winner сохраняется. Конкурентный writer,
  проигравший заполнение canonical row, сохраняет собственный probe-bearing
  result, пока тот выбран его work; cache winner не подменяет selected result.
- `media_fingerprint_result` хранит неизменяемые compressed fingerprint, фактические
  версии fpcalc/Chromaprint, algorithm namespace, duration, время, provenance и
  parser contract. Отдельный `media_fingerprint_cache` имеет PK
  `(source_sha256, fpcalc_version)`, FK RESTRICT на canonical variant digest и
  composite FK RESTRICT `(result_id, fpcalc_version)` на result. Первый успешный
  cache insert побеждает; повтор той же версии cache не перезаписывает. Selected
  result не уникален по variant/version. У variant без SHA прежний success живёт
  до успешной замены; ошибка его не перезаписывает.
- `source_analysis_work` — текущая работа на location: UUID, unique
  `location_id`, root, immutable configured/inventory/relative paths, size/mtime,
  scan-sampled SHA policy, scan-operation provenance без FK и creation time.
  `(location_id, source_root_id)` защищается unique key и composite FK; изменение
  identity создаёт новый work UUID, backfill нет.
- `source_analysis_step` имеет PK `(work_id, step)` и work FK CASCADE; хранит
  независимый latest attempt/status, delivery, safe error, успешный result и
  reuse provenance. Execution operation/attempt/job либо заполнены все вместе,
  либо все NULL; queued/running требуют тройку, failed — непустую safe error,
  skipped — reason. Step-specific successful result FKs — RESTRICT; terminal,
  queued/running/failed могут удерживать предыдущий success. Provenance operation
  UUID не FK на очищаемую operation. Это current state, не история; retry отказа
  его не меняет.
- Operation snapshot использует nullable `source_analysis_mode` (`batch` /
  `single_step`), target work/step и `tools_read_required`. Scan несёт active root,
  но не analysis target; batch — active root без location/work/step; single-step
  — root/location/work/step, terminal target поля nullable. Admission и constraint
  trigger проверяют принадлежность work/location/root. На active source operation
  deferred constraint trigger требует tool-read hold тогда и только тогда, когда
  `tools_read_required`; `operation_tool_read_hold` имеет PK
  `(operation_id, installation_id)` и FK RESTRICT для обоих ключей; terminal
  удаляет holds атомарно. Исключение tools-move учитывает только
  `tools_read_required`, без fallback. Hash-only работа не зависит от tool move.
- `operation_source_work_hold` с PK `(operation_id, work_id)` и FK RESTRICT
  удерживает batch target; execution pair ссылается на hold. Terminal сначала
  очищает execution triples, затем holds. Delivery — `operation.river_job_id`;
  существующий unique `(operation.id, attempt, river_job_id)` служит fence.
  Удаление operation не удаляет work.

Scan публикует private SHA/probe/fpcalc candidates только после полного успешного
traversal. Atomic apply проверяет operation/attempt/root, reconciles candidates,
обновляет identity и independent step state, создаёт durable pending intent,
завершает scan и удаляет candidates; ошибка откатывает публикацию целиком.
Подготовленный fpcalc output — typed candidate и публикуется вместе с выбранным
успешным fingerprint независимо от probe; matching eligibility не определяет
сохранение результата. Отдельная admission transaction создаёт batch и River job
из committed intent.

Для canonical digest применять `INSERT ... ON CONFLICT DO NOTHING`, затем
отдельный `SELECT` при READ COMMITTED: один CTE snapshot может не увидеть
конкурирующий insert. Проверять размер canonical row; mismatch — conflict, не
молчаливое объединение. При успешном hash retry переключать identity, не меняя
уже успешные selected probe/fingerprint siblings; заполнять лишь отсутствующие
canonical cache successes и чистить после обновления consumers. Cleanup digest-less
variant проверяет все inbound references (locations, current successes, live holds
и другие domain refs); digest orphan остаётся cache. Provenance UUID и snapshot
сами по себе holds не создают.

В batch достаточно одного orchestration River job, без child jobs; step attempt
входит в delivery fence. Recovery восстанавливает недоставленный pending intent,
но не persisted failure или committed success; старые deliveries не применяются,
повтор terminal delivery — no-op. Успешный probe retry может добавить только
недостающий fingerprint intent. Общий порядок блокировок: tools-move gate для
tool-dependent admission → package selection → roots → locations/work →
installations → operations/steps → digest/variants/results → writes. UUID-наборы
сортируются; filesystem execution идёт вне DB transaction. SHA-only path не берёт
tools gate. Root lock удерживается только в короткой transaction, а unrelated
roots остаются независимыми. Per-root active-operation unique index охватывает
все source operations.

Managed fpcalc вызывается как `-json -- ABSOLUTE_SERVER_SOURCE_PATH`, без shell,
PATH, client paths/version и `-length`; используется встроенная длительность
120 секунд, без дополнительного backend-лимита и обещания full-file fingerprint.
Перед/после проверяется stat. Down-миграции до изменений проверяют небезопасные
строки и отказывают без destructive guesses; безопасный down удаляет только свои
artifacts. Реализация схемы разбита на этап 2 (results/cache, work/step, holds,
fences и apply APIs) и этап 3 (единая operation shape, guards, admission/retry/
recovery/terminal и lock ordering); down каждого этапа возвращает предыдущую
shape и отказывает при небезопасном состоянии.

**Готово, когда:** reviewer принимает таблицу «текущее → target → миграция» и
решение по hash/shared media identity, текущим операциям и private preparation/
fingerprint eligibility integration. Поведение no_audio соответствует уточнению
владельца от 2026-10-05 выше; новые правила probe_error не вводятся.

### 2. Ввести persistence-модель результатов, hash identity и идемпотентные apply

**Зависимости:** 1. **Область:** новые миграции, persistence/models/repos/tests.

Техническая форма схемы описана в подразделе «Техническая форма целевой схемы»
шага 1 выше. Первое review пункта 1 выполнено 2026-10-05. После уточнения
владельца об отсутствии legacy проект упрощается и требует повторного review;
прежний PASS не означает приёмку изменённой схемы или реализованной миграции.

Добавить только согласованную схему. При включённой SHA-настройке SHA-256
вычисляется для новых locations и изменившихся источников; не заполнять hash у
старых неизменённых locations без digest. Для неизменённой пары допустим reuse
только актуального digest. При выключенной настройке hash нейтрально пропускается;
отсутствующий digest хранится как NULL. Setting transition сохраняет существующие
результаты и не запускает массовый rehash.
Запись результатов каждого шага и состояния retry выполняется независимо и
транзакционно. Shared identity создаётся/переиспользуется конкурентно без
дублирующих hash rows; корректно связывает новый location с существующей
identity. Если при совпадающих SHA и fpcalc version fingerprint уже есть,
выдать его как сохранённый provenance без повторного вычисления.

Старый result не удаляется при неуспехе. Нельзя автоматически merge-ить старые
digest-less variants на основании fingerprints. Изменившийся source не должен
прикрепить результат предыдущего размера/mtime. Cleanup учитывает locations,
references и активные operations/read holds; не полагаться на CASCADE как на
неявную семантику media lifecycle.

**Готово, когда:** чистая миграция и rollback актуальной схемы,
уникальный concurrent insert одного digest, повторный apply, stale identity,
failure после успешного sibling step и сохранение references актуальных
результатов покрыты PostgreSQL tests. Upgrade fixtures прежней модели не
требуются. Тесты также доказывают, что no-hash location не deduplicates и
не переиспользует fingerprint, а stale digest не участвует в identity/reuse.
Прежние locations не получают fabricated digest/fingerprint.

### 3. Реализовать durable step operations и tool read holds

**Зависимости:** 1–2. **Область:** analysis start/service, operation snapshot /
dispatch, persistence enqueue, tool mutation boundaries, River workers.

Выбрать минимальную актуальную схему operation для `analyze_source`, без
параллельного compatibility executor.
Atomic durable start/enqueue для требуемых шагов использует server-resolved
installation IDs только для необходимых tool steps, единую актуальную форму snapshot и
`(root, location, size, mtime)`. Hold нужного tool сериализуется с delete/move;
retry конкретного failed шага восстанавливает только необходимый hold и durable
job атомарно. SHA retry не требует installation. Fingerprint retry использует
сохранённый актуальный ffprobe result, когда он доступен. Успешные sibling steps
не ставятся заново. Ошибочный/отказавший retry
не меняет snapshot, не создаёт job и оставляет прежний результат доступным.

Определить lock ordering относительно scan, setup tools activation/move/delete,
source root mutations и concurrent retries. Независимые roots и операции, не
использующие один ресурс, остаются независимыми. Использовать один актуальный
dispatch/worker; schema migration не запускает массовый анализ или backfill.

**Готово, когда:** PostgreSQL/River integration tests доказывают atomic enqueue,
commit/rollback, duplicate delivery, holds только используемых installations,
оба порядка
конкуренции с move/delete/retry и выполнение актуального snapshot. Нет job без
durable target или operation без recoverable job/intent.

### 4. Выделить шаги ffprobe, SHA-256 и fpcalc с независимым сохранением

**Зависимости:** 2–3. **Область:** `integrations/tools/`, source analysis
service, worker, тесты.

Если SHA toggle включён, сначала вычислить SHA-256 отдельным последовательным
чтением source в in-place mode и выполнить digest lookup успешного probe.
Cache miss запускает недостающие полноценный `ffprobe` и `fpcalc` параллельно;
`fpcalc` разрешён до получения audio eligibility. Его успешный output/provenance
сохраняется при ошибке probe и доступен для cache reuse по `(SHA-256, fpcalc
version)` независимо от probe; single-audio gate ограничивает только matching:
fingerprint не участвует в matching, пока выбранный успешный актуальный `ffprobe`
не подтвердит ровно один audio stream. Успешный probe без ровно одного audio
stream не получает используемый для matching fingerprint: состояние остаётся
нейтральным unsupported/skipped для matching, не failure, а успешный fingerprint
не отбрасывается. Внутренняя модель private typed candidate → atomic publish
выбранного успешного fingerprint не зависит от probe success и eligibility;
техническому review подлежит конкретная реализация этой модели, а не допустимость
публикации успешного результата по audio eligibility. При выключенном toggle
hash нейтрально пропускается, а инструменты продолжаются без hash. Повторный
stat перед/после сохраняет текущую freshness модель, не вводя новый продуктовый
барьер обнаружения bytes при прежнем stat.
Каждый step возвращает typed result/error и достаточный provenance (версия
реального executable для tool steps); не публиковать успешный fingerprint с
ошибочным/пустым output.

Не добавлять эвристики для выбора stream, дополнительного ограничения длительности
со стороны приложения/backend, fingerprint длинных/коротких файлов, fallback,
retry или audio policy, которых нет в утверждённом scope. Встроенный default
`fpcalc` (120 секунд) остаётся в силе; полный-файл fingerprint не обещается.
При выявлении необходимого продуктового выбора приостановить только зависимую
часть и запросить решение.

**Готово, когда:** tests на known SHA/fingerprint fixture, ffprobe сохранение,
ошибка каждого инструмента, source stat change, cancellation, output limits и
version provenance. Тестировать enabled и disabled toggle, transitions, neutral
disabled hash с продолжением ffprobe/fpcalc, и reuse только с актуальным
вычисленным digest. Проверить SHA-first probe-cache lookup, параллельный запуск
недостающих probe и fpcalc, fingerprint output при ошибке probe (с запретом
 matching до single-audio подтверждения), а также сохранение успешного
 fingerprint и provenance при zero/multi-audio, включая cache reuse по
 `(SHA-256, fpcalc version)`, с neutral unsupported/skipped только для matching.
 Retry запускает только
адресованный ошибочный шаг.

### 5. Автоматически и надёжно запускать анализ после успешного scan

**Зависимости:** 2–4. **Область:** source scan apply/worker, durable enqueue,
River recovery, root concurrency integration tests.

После commit полностью успешного scan поставить анализ новых/изменённых
locations без отдельного клиентского Analyze. Для overlap полного `ffprobe` и
`fpcalc` scan может выполнить их подготовку до commit в private candidates;
никакие результаты, location associations, inspector state или downstream jobs
не становятся видимыми/долговечными до успешного traversal и atomic apply.
Подготовка, включая уже успешный tool output, discarded при failed/interrupted
scan. В случае failed/interrupted scan не ставить работу по неполному candidate
set и не менять inventory. Использовать
одну transaction для reconciliation + enqueue/intent или доказанный эквивалент;
crash между успешной фиксацией scan и enqueue должен находиться и восстанавливаться
startup reconciliation. Recovery отличает недоставленную работу от failed step:
оно восстанавливает потерянный durable dispatch, но не запускает auto-retry
неудавшегося шага.

Проверить unchanged scan не создаёт повторную работу, changed `size`/`mtime`
инвалидирует hash и соответствующие результаты, новый путь вызывает hash lookup
при enabled setting, удалённый location не получает поздний apply. Не создавать одну работу
на каждый scan generation, если location и target step остаются текущими.

**Готово, когда:** PostgreSQL/River crash-point tests между traversal,
reconciliation, enqueue и job delivery доказывают нет lost work/no partial scan;
повторный scan идемпотентен; startup recovery не анализирует заново уже
применённый шаг и не меняет результаты failed step автоматически.

### 6. Добавить точечный ручной retry и согласованный REST контракт

**Зависимости:** 3–5. **Область:** API, operation dispatch, read model,
OpenAPI/Orval-generated client, Settings API/runtime setting, API tests.

Settings API читает и изменяет глобальный SHA-256 toggle как typed runtime
setting, сохранённый в PostgreSQL; client не задаёт setting через env. Если hash
не вычислялся, digest остаётся null/отсутствующим, а не fabricated. Setting
сэмплируется при создании scan operation и фиксируется в её durable snapshot;
последующий analysis work наследует policy, explicit retry failed hash сохраняет
исходную policy. Переключение не меняет поставленную работу и не создаёт hash
intent для неизменённых locations без digest. GET
inspector возвращает независимый статус шагов, сохранённые данные/версии,
активную operation и актуальную active fpcalc version. Retry принимает только
конкретный поддерживаемый failed step и текущую expected identity; client не
передаёт arbitrary path/version/executable. Отдельный точечный rerun fingerprint
адресует только fingerprint текущего актуального location и выполняет его
текущей active `fpcalc`; он не передаёт произвольный executable/version и не
повторяет ffprobe/SHA или весь pipeline. Пока rerun идёт, прежний успешный
fingerprint остаётся читаемым; неуспешный rerun не заменяет его и не оставляет
частичного результата. Существующий Retry общего operation не должен случайно
повторять уже успешные шаги. Смена active fpcalc сама по себе не создаёт массовых
операций.

**Готово, когда:** API tests проверяют enabled/disabled setting persistence and
readback, актуальные и malformed snapshots, 404/409/503
границы, stale retry, неправильный шаг, успешный и неуспешный step retry, явный
rerun fingerprint успешного результата прежней версии текущей active `fpcalc`,
сохранение прежнего fingerprint при неудаче, tool version display, shared hash
result; generation pipeline синхронизирует OpenAPI и TypeScript клиент.

### 7. Обновить инспектор и состояния анализа

**Зависимости:** 6. **Область:** frontend Settings UI и sources inspector,
React Query, component tests.

Добавить в Settings UI переключатель глобальной SHA-256 настройки с пояснением,
что он влияет на последующие анализы, не удаляет сохранённые результаты и не
запускает массовый rehash. Значение читается/изменяется через typed Settings API
и PostgreSQL, не из env.
Удалить обязательность отдельной кнопки анализа обычного нового/изменённого
location. Показывать состояние ffprobe/hash/fingerprint отдельно, прежние
успешные данные даже если следующий шаг failed, точечный retry только для
конкретной ошибки, actual analysis version и current active fpcalc version.
Отдельно показать явное ручное действие rerun fingerprint, когда сохранённый
успешный fingerprint получен прежней версией, и сохранять его на экране, пока
идёт rerun или пока rerun неудачен. Для zero/multi-audio показывать matching
eligibility как нейтральное unsupported/skipped, без retry ошибки matching.
Успешный шаг fpcalc сохраняет success, fingerprint и provenance; доступные
probe и hash также сохраняются. Disabled hash показывать нейтрально skipped,
не ошибкой; ffprobe/fpcalc продолжаются. При duplicate hash показать
shared/reused результат только для актуального вычисленного digest; без hash
показывать независимую location. Не утверждать уникальность fingerprint или
автоматически добавленную track association. SSE
остаётся wake-up, после него перечитывается detail. Сохранить disconnect/reload
и discovery активной работы. Не добавлять matching/group/publication UI.

**Готово, когда:** RTL покрывает Settings toggle enabled/disabled and persistence
after reload; scan → automatic queued/running/success,
partial success + fpcalc failure, step-only retry, явный rerun fingerprint
прежней версии с сохранением прежнего результата при неудаче, zero/multi-audio:
fpcalc success и сохранённый результат отдельно от unsupported/skipped matching,
version change lazy state, shared hash reuse,
source changed/stale, active reconnect, частичный ffprobe-only результат и
отказ/ошибки без потери прежних результатов.

### 8. Обновить документацию и провести независимую приёмку

**Зависимости:** 1–7. **Область:** README, design docs, this plan/evidence.

Актуализировать runtime documentation: ручной scan остаётся; automatic step
analysis реализован; перечислить точные не реализованные зоны. Не переписывать
done планы 05/06 и их приёмочные отчёты. Запустить `task verify`; пройти реальный
PostgreSQL/River сценарий с managed tools, включая новое/изменённое/неизменённое
содержимое, retry только ошибки, restart recovery, duplicate SHA reuse и lazy
fpcalc version update. Reviewer независимо сопоставляет каждый критерий с кодом,
tests и UI.

**Готово, когда:** полный gate exit 0; evidence на конкретной revision; reviewer
COMPLETE. Ограничения среды и непроверенные platform cases явно перечислены.
Только после COMPLETE переносить план из `todo/` в `done/`.

## Критерии готовности

| Критерий | Доказательство |
| --- | --- |
| SHA-256 управляется typed PostgreSQL runtime setting через Settings UI без env. | Шаги 3, 6–7: Settings/API persistence, toggle и reload tests. |
| Enabled вычисляет SHA только для новых и изменившихся источников, не заполняя hash старым неизменённым locations; disabled нейтрально пропускает hash, продолжая ffprobe/fpcalc; отсутствующий digest — NULL; переходы не удаляют результаты и не делают массовый rehash. | Шаги 2–5, 7: enabled/disabled/transition tests, отсутствие bulk jobs и сохранность данных. |
| Без актуального вычисленного SHA locations независимы; устаревший digest не участвует в reuse/dedup; fingerprint не является identity. | Шаги 1–2, 4, 6–7: no-hash/stale-hash and reuse tests. |
| Нынешний manual scan автоматически и durable ставит анализ только после полного успеха. | Шаги 3, 5, 8: transaction/crash recovery и failed scan tests. |
| `(size, mtime)` определяет freshness; при включённом toggle SHA пересчитывается при изменении stat без требования bytes-proof при прежнем stat. | Шаги 2, 4, 5: exact stat interleavings. |
| Успехи ffprobe/hash/fingerprint независимы и сохраняются при чужом failure; успешный fingerprint доступен для cache reuse по SHA-256+fpcalc version независимо от probe, а в matching участвует только при выбранном успешном актуальном ffprobe ровно с одним audio stream; ручной retry точечный, явный rerun fingerprint сохраняет прежний результат. | Шаги 2–4, 6–7: partial apply и worker/API/UI tests. |
| Только single-audio-stream файл получает fingerprint, используемый для matching; при zero/multi состояние нейтрально unsupported/skipped для matching без отбрасывания успешного fingerprint, с probe и hash если включён, без retry; no_audio хешируется при enabled согласно решению владельца. | Шаги 1, 4, 6–7: product decision и tests. |
| Тот же актуальный SHA+fpcalc version переиспользует результат только при вычисленном hash; без hash locations независимы, SHA не выступает fingerprint/recording identity. | Шаги 1–2, 4, 6–7: concurrent dedup/provenance and no-hash tests. |
| Смена active fpcalc не вызывает массовую переобработку; UI показывает actual/current versions. | Шаги 3, 6–7: tool swap/version UI test и отсутствие bulk jobs. |
| Актуальная модель переживает restart/retry; migration и rollback не фабрикуют результаты. | Шаги 1–3, 6, 8: migration/River tests актуального контракта. |
| Источники read-only; holds нужны только используемым tools, чужие roots/уже сматченные и опубликованные состояния не портятся. | Шаги 3–5, 8: source byte evidence, lock/race tests, references audit. |
| Scope остаётся без scheduled scan/auto retry/matching/groups/publication. | Шаги 5–8: независимый review документации, API и diff. |
| `task verify` и независимая приёмка завершены. | Шаг 8: gate evidence и COMPLETE report. |

## Уточнённые вопросы владельца

Вопрос `no_audio` закрыт владельцем в сессии реализации 2026-10-05:
при включённой SHA-настройке hash обязателен и для этих файлов; по актуальному
digest переиспользуется известный результат. Старый variant с digest остаётся
кэшем, без digest действует cleanup после освобождения references/read holds.
Не сохранять неактуальный результат как текущий результат изменённой location.

Для multi-stream отдельного открытого продуктового вопроса больше нет:
по последнему согласованному решению успешный fingerprint сохраняется независимо
от probe, вместе с доступными probe и SHA-256 (если настройка включена).
Только matching eligibility имеет нейтральное unsupported/skipped без retry
ошибки matching; это не отменяет success шага fpcalc и cache reuse.
