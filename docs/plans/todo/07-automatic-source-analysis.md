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

Оператор по-прежнему явно запускает scan. После **полностью успешно
завершившегося** scan новые и изменённые locations автоматически получают
поэтапный анализ: сохранение технического `ffprobe`, опциональный SHA-256 и
Chromaprint через managed `fpcalc`. Отдельно нажимать Analyze на каждом файле не
нужно. Глобальная настройка SHA-256 вкл/выкл — типизированная runtime setting в
PostgreSQL с управлением через Settings UI; новой env-переменной нет. Никакого
scheduled scan этот этап не добавляет.
Setting фиксируется для analysis operation при её старте; последующее изменение
toggle влияет на новые operations, а не меняет уже начатую operation.
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
  Оператор явно повторяет конкретный ошибочный шаг, а не весь анализ.
- Chromaprint рассчитывается только для файла ровно с одним audio stream.
  Смена active `fpcalc` не запускает массовый backfill. UI показывает версию
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
- Существующие ручные ffprobe-only results и durable operations должны остаться
  читаемыми. Старый анализ не считать новым анализом «всех шагов» и не объявлять
  fingerprint/SHA успешно выполненными. Snapshot schema эволюционирует
  backward-compatible; нельзя додумывать недостающие поля старой операции из
  сегодняшних settings.
- Вне scope: scheduled scan, automatic retry/backoff, кнопка полного повторного
  анализа как основное действие, настройки/пороговые условия, не утверждённые
  владельцем (кроме согласованного SHA-256 toggle), staged/work directory, inbox groups, matching, AcoustID,
  MusicBrainz, confidence, локальная медиатека и публикация.

Не менять правила `no_audio`/`probe_error` без отдельного ответа владельца.
Multi-stream больше не открытый продуктовый blocker: fingerprint по согласованию
вычисляется только для single-audio-stream файла. Для файла с несколькими audio
streams `fpcalc` не запускается, сохраняются доступные probe и SHA-256 (если
hashing включён), показывается нейтральное состояние unsupported/skipped, а не
failure; retry ошибки для него не предлагается. Технические proposals ниже должны сохранять успешные допустимые
шаги, не трактуя неисполняемый fingerprint как успешный fingerprint. Новое
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

Шаги последовательны. Исполнитель сначала читает локальные AGENTS.md, текущие
migrations, models, persistence, service, API, worker, generated client и
соседние tests. Названия новых сущностей ниже — понятия контракта, не требование
к именованию. Конфликт актуального кода с контрактом, необходимость нового
продуктового выбора или недостаточная backward compatibility останавливают
зависимый шаг и возвращаются ведущему агенту/владельцу; не исправлять scope
молча.

Локальный build/test gate репозитория — только `task verify`; узкие tests
допустимы только для диагностики его failure. Generated client обновлять штатным
pipeline, не вручную. Коммиты — только по отдельному явному разрешению сессии.

### 1. Сверить физическую модель и спроектировать backward-compatible state

**Зависимости:** нет. **Область:** `docs/design/data-model.md`,
`docs/design/music_ingest_redesign.dbml`, миграции, operation snapshots,
persistence models.

Составить сопоставление текущих columns/constraints/operation snapshots с
целевыми независимыми шагами, shared SHA identity и lifecycle provenance.
Предложить физическую схему: какие результаты принадлежат location/media
identity, как сохраняются per-step ошибки и версии, какие уникальные ключи и
FK соблюдают legacy variant references. Проверить applied migration chain и
rollback на существующих manual analysis rows. Не переписывать старые миграции;
не менять концептуальный DBML так, будто предложение уже применено.

Заранее описать schema-version dispatch: старый snapshot читается исходным
ручным ffprobe-only исполнителем/путём и не получает придуманного SHA/fingerprint;
новый snapshot однозначно описывает target step(s), tool installations и stat
identity. Перечислить terminal/recovery states и FK release behavior.

**Готово, когда:** reviewer принимает таблицу «текущее → target → миграция» и
решение по hash/shared media identity; подтверждена совместимость завершённых,
failed и queued старых операций. Открытый вопрос no_audio передан владельцу до
выбора пользовательского поведения.

### 2. Ввести persistence-модель результатов, hash identity и идемпотентные apply

**Зависимости:** 1. **Область:** новые миграции, persistence/models/repos/tests.

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

**Готово, когда:** чистая миграция, rollback, upgrade с существующими данными,
уникальный concurrent insert одного digest, повторный apply, stale identity,
failure после успешного sibling step и сохранение старых references покрыты
PostgreSQL tests. Тесты также доказывают, что no-hash location не deduplicates и
не переиспользует fingerprint, а stale digest не участвует в identity/reuse.
Прежние locations не получают fabricated digest/fingerprint.

### 3. Реализовать durable step operations и tool read holds

**Зависимости:** 1–2. **Область:** analysis start/service, operation snapshot /
dispatch, persistence enqueue, tool mutation boundaries, River workers.

Выбрать минимальную схему operation compatible с существующей `analyze_source`.
Atomic durable start/enqueue для требуемых шагов использует server-resolved
installation IDs только для необходимых tool steps, versioned snapshot и
`(root, location, size, mtime)`. Hold нужного tool сериализуется с delete/move;
retry конкретного failed шага восстанавливает только необходимый hold и durable
job атомарно. SHA retry не требует installation. Fingerprint retry использует
сохранённый актуальный ffprobe result, когда он доступен. Успешные sibling steps
не ставятся заново. Ошибочный/отказавший retry
не меняет snapshot, не создаёт job и оставляет прежний результат доступным.

Определить lock ordering относительно scan, setup tools activation/move/delete,
source root mutations и concurrent retries. Независимые roots и операции, не
использующие один ресурс, остаются независимыми. Сохранить dispatch/worker для
уже существующих snapshot версий; не превращать schema migration в массовую
перепостановку старых операций.

**Готово, когда:** PostgreSQL/River integration tests доказывают atomic enqueue,
commit/rollback, duplicate delivery, holds только используемых installations,
оба порядка
конкуренции с move/delete/retry и legacy snapshot execution. Нет job без
durable target или operation без recoverable job/intent.

### 4. Выделить шаги ffprobe, SHA-256 и fpcalc с независимым сохранением

**Зависимости:** 2–3. **Область:** `integrations/tools/`, source analysis
service, worker, тесты.

Расширить ffprobe результатом только при нужной причине актуализации, не
переанализировать его потому, что неудачен fpcalc. Если SHA toggle включён,
SHA-256 вычисляется отдельным последовательным чтением source в in-place mode.
Если toggle выключен, hash-step
neutral/skipped, а ffprobe и допустимый fpcalc продолжаются без hash. Повторный stat перед/после
сохраняет текущую freshness модель,
не вводя новый продуктовый барьер обнаружения bytes при прежнем stat.
Chromaprint запускается managed fpcalc только для ровно одного audio stream.
Каждый step возвращает typed result/error и достаточный provenance (версия
реального executable для tool steps); не публиковать успешный fingerprint с
ошибочным/пустым output.

Не добавлять эвристики для выбора stream, fingerprint длинных/коротких файлов,
limits, fallback, retry или audio policy, которых нет в утверждённом scope.
При выявлении необходимого продуктового выбора приостановить только зависимую
часть и запросить решение.

**Готово, когда:** tests на known SHA/fingerprint fixture, ffprobe сохранение,
ошибка каждого инструмента, source stat change, cancellation, output limits и
version provenance. Тестировать enabled и disabled toggle, transitions, neutral
disabled hash с продолжением ffprobe/fpcalc, и reuse только с актуальным
вычисленным digest. Последовательность «ffprobe success → SHA success → fpcalc
failure» оставляет первые два шага доступными; retry запускает только fpcalc.

### 5. Автоматически и надёжно запускать анализ после успешного scan

**Зависимости:** 2–4. **Область:** source scan apply/worker, durable enqueue,
River recovery, root concurrency integration tests.

После commit полностью успешного scan поставить анализ новых/изменённых
locations без отдельного клиентского Analyze. В случае failed/interrupted scan
не ставить работу по неполному candidate set и не менять inventory. Использовать
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
сэмплируется при создании analysis operation и фиксируется в durable snapshot,
чтобы позднейшее переключение не меняло поведение уже поставленной работы. GET
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
readback, старые и новые snapshots, 404/409/503
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
идёт rerun или пока rerun неудачен. Multi-stream location показывать как
unsupported/skipped fingerprint (не failure), без retry ошибки, с сохранёнными
probe и hash, если hashing включён. Disabled hash показывать нейтрально skipped,
не ошибкой; ffprobe/fpcalc продолжаются. При duplicate hash показать
shared/reused результат только для актуального вычисленного digest; без hash
показывать независимую location. Не утверждать уникальность fingerprint или
автоматически добавленную track association. SSE
остаётся wake-up, после него перечитывается detail. Сохранить disconnect/reload
и discovery активной работы. Не добавлять matching/group/publication UI.

**Готово, когда:** RTL покрывает Settings toggle enabled/disabled and persistence
after reload; scan → automatic queued/running/success,
partial success + fpcalc failure, step-only retry, явный rerun fingerprint
прежней версии с сохранением прежнего результата при неудаче, multi-stream
unsupported/skipped без retry, version change lazy state, shared hash reuse,
source changed/stale, active reconnect, legacy ffprobe-only location и
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
| Успехи ffprobe/hash/fingerprint независимы и сохраняются при чужом failure; ручной retry точечный, явный rerun fingerprint сохраняет прежний результат. | Шаги 2–4, 6–7: partial apply и worker/API/UI tests. |
| Только single-audio-stream файл получает fingerprint; multi-stream даёт unsupported/skipped (не failure) с probe и hash если включён, без retry; no_audio policy не выдумана. | Шаги 1, 4, 6–7: product decision и tests. |
| Тот же актуальный SHA+fpcalc version переиспользует результат только при вычисленном hash; без hash locations независимы, SHA не выступает fingerprint/recording identity. | Шаги 1–2, 4, 6–7: concurrent dedup/provenance and no-hash tests. |
| Смена active fpcalc не вызывает массовую переобработку; UI показывает actual/current versions. | Шаги 3, 6–7: tool swap/version UI test и отсутствие bulk jobs. |
| Старые ffprobe-only rows и operation snapshots переживают миграцию, restart и retry. | Шаги 1–3, 6, 8: legacy fixtures + migration/River tests. |
| Источники read-only; holds нужны только используемым tools, чужие roots/уже сматченные и опубликованные состояния не портятся. | Шаги 3–5, 8: source byte evidence, lock/race tests, references audit. |
| Scope остаётся без scheduled scan/auto retry/matching/groups/publication. | Шаги 5–8: независимый review документации, API и diff. |
| `task verify` и независимая приёмка завершены. | Шаг 8: gate evidence и COMPLETE report. |

## Открытые вопросы владельца / блокирующие продуктовые решения

1. Что считается продуктовым outcome для `no_audio` location при automatic step
   analysis: только SHA identity (если toggle включён) либо полностью вне
   автоматической очереди?
   Сохраняется ли прежний успешный digest/result, если тот же путь после изменения
   стал `no_audio`?

Для multi-stream отдельного открытого продуктового вопроса больше нет:
fingerprint по согласованию не вычисляется, доступные probe и SHA-256 (если
настройка включена) сохраняются,
состояние unsupported/skipped не является failure и не предлагает retry. До
ответа на вопрос 1 исполнитель может делать persistence/API seams, но не
закреплять неоднозначное поведение как requirement, status semantics или
acceptance result.
