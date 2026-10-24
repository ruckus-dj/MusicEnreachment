# План 12 — файловая публикация

## Упрощение по прямому решению владельца — 2026-10-23

Этот раздел фиксирует действующий контракт исполнения; при противоречии ему
технические варианты ниже не являются требованиями. Durable River job (только ID
операции) запускает полную свежую подготовку целого результата во временном файле
в приватной области рядом с фактическим destination на той же файловой системе.
Результат валидируется/readback-проверяется, затем native atomic replace меняет
destination. После этого одна DB-транзакция записывает actual projection и успех
операции, освобождает holds и потребляет attempt; только после подтверждения этого
commit допустима отдельная opt-in source cleanup.

Сбой до DB commit повторяет подготовку целиком и допускает повторную замену уже
заменённого target. Exactly-once filesystem effect, пошаговые journal/ACK,
backup/rollback, resume и boot recovery не требуются и не должны достраиваться.
SQL-транзакция не охватывает ffmpeg. Общий coordinator сериализует публикацию с
конкурентным сохранением/изменением результата (включая reset/delete), а также
не допускает stale manual selection или перезапись target другим owner.
Source-free rewrite читает текущий destination; lyrics и иные metadata updates
не должны lossy-reencode audio или терять artwork.

Atomic replace поддерживается только native-операцией на той же FS; delete-then-copy
fallback запрещён. Универсальная power-loss durability не обещается, а directory
sync не является условием атомарности replace. При смене пути или расширения
артефакта в пределах текущего output сначала фиксируется новый DB result; в той же
транзакции результата при необходимости создаётся durable cleanup intent/job, а
фактическое удаление прежнего managed файла выполняется после commit отдельно, со
свежими проверками и без post-commit enqueue gap. Ошибка cleanup — обычное
предупреждение, не source cleanup. Смена или сброс output root сюда не входят:
старые физические файлы не удаляются. Source cleanup по умолчанию выключен: перед
началом перечитывается opt-in и проверяются consumers; завершённая job при повторной
delivery не делает файловую работу даже при отсутствующем source. Cancellation и
исчерпание retry дают видимую ошибку/ручной retry, не вечный running и не ранний
cleanup. Достаточно ожидать основной процесс и собственные callbacks, без
доказательства остановки дерева процессов. Идентичность байтов whole-file при
повторной подготовке не требуется; временное file/DB расхождение после сбоя до
commit и повторная работа допустимы. Внутренняя dev-совместимость V1/V2/V3 не
требуется. Неоднозначные продуктовые вопросы задаются владельцу.

Датированные записи прогресса ниже — свидетельства о прежних конкретных срезах,
а не описание текущего контракта или критерии приёмки; их исторические факты не
переписываются. Противоречащие текущему контракту технические требования ниже
заменены в D07/D08, срезах и проверочной матрице.

## Краткий текущий статус

**2026-10-24, реальная приёмка применения обложки на ревизии `8f9ac36`:** на
приватном стенде подтверждён сквозной manual pending cover → release-wide apply →
derivative embedding для трёх уже опубликованных MKA. Baseline первой публикации
выполнен эквивалентным `98434c4` бинарником; затем собрано исправление API body
limit (`8f9ac36`), приложение перезапущено, и cover-этап выполнен на нём. Исходный
PNG 4096 × 4096 (24 121 925 bytes) сохранён в БД как selected generation
`a17b1c3f-77c3-4006-8ab4-d5dfd6816768`; bytes сверены независимым read-only SQL,
`pending` отсутствует. Все три apply-операции завершились успешно. Во всех
результатах фактическая обложка независимо извлечена ffmpeg как 2048 × 2048 PNG
с одинаковым SHA-256, отличным от оригинала; encoded packets и все
format/audio stream tags, включая lyrics, не изменились. Navidrome 0.64.2
отдаёт `getCoverArt` 2048 × 2048 с тем же SHA-256. Это приёмка cover-сценария, а
не всего плана: исходные MKA содержали только audio без embedded artwork, поэтому
захват embedded artwork при анализе исходника этим прогоном не доказан. Полный
`task verify` и hook прошли для `8f9ac36`. Последующая ревизия `61b481c`
полностью прошла CI `40078308182`: все пять платформ, verify, Docker smoke и
публикация контейнера. Остаются автоматические обновления качества,
provider-защищённые теги, wakes и очистка устаревших output.
Подробности — [отчёт о приёмке обложки](../../reports/plan12-cover-acceptance-2026-10-24.md).

**2026-10-24, foundation очистки устаревших output:** добавлены схема
`obsolete_publication_output_cleanup`, транзакционный
admission только вместе с реальным River insert (в args — лишь ID операции),
guarded callback под DB-локами operation/authority/namespace, запрет повторного
использования старого canonical пути при живой cleanup authority, отзыв authority
и отмена доставок при output reset, а также регистрация service/jobs worker в
`app.Run`. Это foundation, а не завершённая очистка: `CommitPublicationResult`
пока не создаёт cleanup authority и не связан с admission, поэтому автоматическое
удаление прежнего файла не включено; автоматические quality upgrades намеренно
остаются выключенными. PG-тесты покрывают rollback admission с реальной вставкой
River job и успешное/отказное терминальное завершение без filesystem-работы при
stale delivery, а также схему и malformed authority, rebinding, reset и reuse
старого пути; native-тесты — nested пути, hash, missing leaf, неверный parent и
ошибку чтения settings из БД. Полный `task verify` прошёл; CI этого foundation
ещё не подтверждена. Следующий
шаг — атомарная связка нового CURRENT результата с admission cleanup; без неё
очистка устаревших output не считается завершённой. Продуктовые решения и
требования этим не меняются; факты CI `61b481c` выше относятся только к той
ревизии, а не к незакоммиченному дереву.

**2026-10-24, уточнение владельца — оригинал обложки и derivative:** оригинальные
bytes обложки сохраняются в анализе/БД без изменений, если каждая сторона
изображения не превышает 10000 пикселей; изображение больше лимита хотя бы по
одной стороне не блокирует аудио, допускает необязательную ошибку, а выбранная
обложка остаётся выбранной. В публикуемый файл встраивается derivative максимум
2048 × 2048, aspect fit, без upscale; файловая обработка через libvips без полного
массива пикселей в Go. Владелец отдельно допустил внутренние native-буферы
декодеров для отдельных форматов; жёсткий предел RAM не обещается, отдельный
процесс с лимитом памяти не выбран. Lossy JPEG и default byte cap не
авторизованы. Начальный выбор обложки не меняется: провайдер, затем максимальное
разрешение сохранённого изображения, при равенстве — минимальный ID кандидата.
Сохранение оригинала, derivative и обработка превышения лимита реализованы.
Полный локальный `task verify` прошёл; после него дополнительно исправлена
совместимость BMP с libvips 8.14 через безопасную приватную файловую копию.
Итоговая проверка поддерживаемых платформ ещё не завершена; план не завершён.
CI `40070030348` для `7913667`: Linux amd64/arm64, macOS x64/arm64, общий verify
и Docker smoke прошли; Windows завершил компиляцию native-библиотек, но упал на
копировании ошибочно названного `COPYING` вместо `LICENSE`. Windows-тесты и
публикация контейнера не выполнены; исправляется staging notice, gates сохранены.
Авторитетная запись — в
[decisions.md](../../design/decisions.md).

**2026-10-24, уточнение владельца — quality upgrade и метаданные; CI `54578ef`:**
автоматическое обновление опубликованной позиции ради улучшения качества
переписывает только аудиодорожку. Все CURRENT теги, lyrics и обложка сохраняются;
текст — часть метаданных и при quality upgrade заново не разрешается. После первой
публикации автоматическая замена метаданных выполняется только при обновлении от
провайдеров (теги) и только для конкретного тега, не изменённого вручную; явные
manual workflows (manual lyrics, pending cover apply и другие утверждённые
действия) сохраняются. Обложка: приоритет провайдера; при его отсутствии —
сохранённая в БД обложка локального источника; из нескольких сохранённых
кандидатов локального источника выбирается максимальное разрешение фактически
сохранённого изображения (ширина × высота в пикселях), при равенстве — минимальный
ID кандидата. Аудио-характеристики для выбора обложки не используются. Это
заменяет правило A.14 о стабильности embedded cover по порядку позиций релиза;
поздний поиск обложки в исходнике не выполняется. Полная CI
`54578efc06488df490554656c20a46db6a03b7c1`, run `40019882848`, завершилась
успешно: verify, Linux amd64/arm64, macOS x64/arm64, Windows x64, Docker Compose
smoke и публикация container image. Это проверка среза, а не приёмка:
acquisition, automatic updates и runtime acceptance остаются незавершёнными,
план не завершён.

**Manual cover, текущий срез на базе `0c0dba3`:** реализованы отдельный pending
save, read/image API, явные release-wide apply/retry и UI через generated client.
Промоция pending и admission всех текущих опубликованных позиций выполняются
одной транзакцией; занятая позиция отклоняет admission целиком, без молчаливого
пропуска. Retry использует selected image и не применяет новый pending.
Source-free rewrite использует CURRENT и cover-only native writer без записи
тегов, сохраняя audio payload, tags и lyrics. Результат остаётся observed;
warning не выдаётся за успешное embedding. Manual-lyrics receipt переносит
только published generation, не подтверждая более новый сохранённый текст.
Полный `task verify` прошёл (340 frontend tests, backend integration, tools,
lint, generate, build); независимое ревью batch/receipt/result не нашло P1/P2.
PG regressions проверяют fanout, реальный второй River insert с rollback всей
транзакции, busy conflict, selected retry и receipt continuity. Нативные тесты
проверяют FLAC/MP3/M4A/MKA; это не заменяет итоговую runtime/Navidrome acceptance.
CI новой ревизии ещё не подтверждена. Acquisition из provider/DB candidates,
automatic quality updates и окончательная приёмка плана остаются незавершёнными.

**CI `0c0dba3`:** полный run `40012105550` завершился успешно: verify,
Linux amd64/arm64, macOS x64/arm64, Windows x64, Docker Compose smoke и
публикация container image. Подтверждён срез analysis → БД → frozen publication
inputs. Текущие manual cover API/UI/apply/retry ещё не входят в эту ревизию;
их интеграционная проверка и итоговая runtime acceptance остаются впереди.

**Уточнение владельца от 2026-10-23 — входы только из анализа/БД:** все source
metadata, включая lyrics, artwork bytes и технические сведения, извлекаются ещё
при анализе. Матчинг связывает сохранённые данные с recording/track MBID или
локальными ID; публикация берёт из исходника только аудиодорожку. Позднего поиска
обложек/тегов/lyrics в связанных исходниках не должно быть. Это заменяет прежнюю
постановку вопроса о late embedded fallback, но не выбирает приоритет между
сохранёнными кандидатами. Проверки CURRENT destination и готового кандидата
сохраняются. В `4d6a65d` initial-lyrics preparation ещё читал metadata/duration
временной копии source: это был разрыв реализации, а не утверждённое исключение.
Датированные результаты проверок ниже не означают
приёмку этого data flow. Авторитетное уточнение — в `docs/design/decisions.md`.

**2026-10-23, source metadata flow на базе `4d6a65d`:** анализ сохраняет
нормализованные TagLib tags/lyrics, properties и inspected cover bytes/MIME/hash;
matching закрепляет metadata-result FK на текущей track-source association.
Publication admission замораживает tags и точную длительность из сохранённого
ffprobe JSON. Initial lyrics больше не открывает source/copy для metadata lookup.
Первый аудиорезультат строится existing selected-stream plan; `source` использует
stream copy без повторного кодирования. CURRENT rewrite сохраняет native copy.
Полный `task verify` прошёл; повторное независимое ревью не нашло P1/P2 в этих
изменениях. PG regression проверяет точность DB hydration и неизменность frozen
lookup после canonical refresh; нативные тесты clearing/replacement artwork
сохраняют encoded payload для FLAC/MP3/M4A/MKA. Это не исчерпывающая инвентаризация
всех raw tags/картинок: существующий TagLib capture contract возвращает
нормализованные tags и первую нормализованную картинку. CI новой ревизии и
итоговая runtime acceptance ещё не подтверждены; план не завершён.

**Проверка `4d6a65d`:** полная CI `39999342103` завершилась успешно: verify,
Linux amd64/arm64, macOS x64/arm64, Windows x64, Docker Compose smoke и публикация
container image. Это подтверждает frozen-cover/whole-job срез и исправленные
Windows assertions, но не устраняет указанный выше разрыв source metadata flow
и не означает завершения плана.

**2026-10-23, следующий срез на базе `5da437d`:** frozen effective cover первой
аудиопубликации подключён к private preparation; actual cover и bounded embedding
warning сохраняются в одной транзакции результата и доступны через read model/API.
Полный `task verify` прошёл. Реальные TagLib/PG regressions проверяют A после
смены release selection на B, сохранение lyrics и encoded FLAC frames, восстановление
приватного baseline при optional cover failures и carry-forward текущего summary.
Probe transport в этих тестах контролируемый: это не приёмка реального ffprobe
для всех контейнеров. CI `5da437d` прошла все платформы, кроме Windows: три старых
теста ожидали снятый directory-sync отказ; их assertions приведены к обычному
успешному nested publication. Полная CI новой ревизии ещё не подтверждена.
Acquisition, manual cover apply/retry UI, automatic updates и runtime acceptance
остаются незавершёнными; порядок fallback источников этим срезом не выбран.

**2026-10-23, упрощённое исполнение:** рабочее дерево на базе `c2e3ce1`
прошло полный `task verify` (generate, lint, backend integration, frontend/tools,
build). Живой executor использует whole-job preparation и прямую транзакцию
результата. Проверены повтор после replace/SQL rollback, redelivery после успеха
без исходника, same-attempt River redelivery, общий process-local coordinator
с metadata save/output reset, terminal settlement и отдельный publication retry
с транзакционной передачей source hold. Это локальная проверка среза, не итоговая
приёмка плана; CI этой новой ревизии и runtime acceptance ещё не подтверждены.
Frozen cover receipt пока не подключён к embedding в простом executor; scope
cover, automatic updates и окончательная приёмка ниже остаются незавершёнными.

**2026-10-23, применение pending cover:** владелец уточнил, что применение новой
pending-обложки обновляет все уже опубликованные позиции этого релиза, а не
выбранное подмножество; сохранение pending само по себе ничего не применяет.
Исполнение опирается на уже утверждённую архитектуру: одна обложка на релиз в
release-level authority, admission и `River.InsertTx` одной Bun-транзакцией,
отдельная job на позицию. Атомарная промоция pending и постановка jobs на
затронутые позиции реализуются позже; это техническая реализация, а не новое
продуктовое требование. Существующий admission отклоняет занятую позицию;
batch откатывается целиком, не пропуская её молча. Решение от 2026-10-23 о
source metadata (анализ → БД) сохраняется; авторитетная запись — в
[decisions.md](../../design/decisions.md).

План **не завершён**. First-publication happy path и manual lyrics MKA/MP3
проверены на реальном стенде с Navidrome. Automatic matching wake и
original-owner cleanup после returned validation failure прошли полный CI.
Разделение canonical source/probe реализовано в `f8e02dc`; его CI отслеживается.

Оставшийся scope сгруппирован так:

1. Source-free lyrics retry: настоящий executable lifecycle, explicit API/UI,
   исходник не требуется; завершение format/artwork preservation coverage.
2. Cover: current release authority, acquisition/embedding, manual pending/apply
   и retry; одна обложка на релиз, без sidecars. Применение pending cover
   обновляет все уже опубликованные позиции релиза, а не выбранное подмножество;
   сохранение pending не применяет (решение владельца 2026-10-23). Сохранение
   оригинала в пределах 10000 × 10000, потоковая подготовка derivative
   ≤2048 × 2048 (aspect fit, без upscale) и обработка превышения лимита реализованы;
   используется libvips с допустимыми native-буферами (решение владельца
   2026-10-24). Реальная приёмка cover-сценария подтверждена 2026-10-24;
   остаётся полная платформенная CI.
3. Automatic updates: независимая source authority, доказанное quality improvement,
   new-source replacement, остальные invalidation producers и безопасные wakes.
4. Whole-job retry/crash matrix и итоговая приёмка всех перечисленных сценариев.

Частичные изолированные реализации и их промежуточные green gates не означают
готовность этих пунктов. Детальные требования и датированные свидетельства ниже
сохранены; актуальные результаты — в [карте выполнения](../../reports/plan12-execution-map-2026-10-20.md).

**Уточнение владельца от 2026-10-22:** повторный поиск текста использует metadata
и длительность текущей публикации, а применение — текущий опубликованный файл.
Исходник не требуется; его удаление очисткой не блокирует эти действия.
Правила явного действия и приоритета ручного текста/explicit-empty сохраняются.
См. [зафиксированное решение](../../design/decisions.md).

**Сверка от 2026-10-20:** выполнение продолжается; полная приёмка ещё не достигнута.
Актуальная [карта реализации](../../reports/plan12-execution-map-2026-10-20.md)
отделяет работающие foundation/vertical slices от оставшегося scope. Полный
`task verify` исходной ревизии `38c57e6` прошёл; это не проверка последующих правок
и не native CI/runtime acceptance. Датированные записи ниже сохранены как история.

**Текущее состояние на 2026-10-18: подготовительная реализация.** Продуктовый
контракт согласован в A.1–A.16. Независимое review
[технического дизайна](#publication-technical-design) разрешило
срезы identity/read model, metadata/primary artist и unified reader/native
removal. Первоначальное описание DRAFT ниже сохранено как контекст подготовки;
оно не отменяет поздние решения. Exposure/tool mutation допускаются только
после соответствующих технических гейтов и проверок; завершение плана и
runtime acceptance ещё не достигнуты.

### Текущий прогресс реализации — 2026-10-18

Незакоммиченный first-publication vertical slice: read-only server preview,
explicit confirmation API/UI, shared deterministic planner и in-transaction
snapshot/native namespace validation до reservations/enqueue. Реальный PG тест
Preview → AdmitFirst → ExecutePublicationDelivery → settlement прошёл локально;
FFmpeg transport в этом тесте контролируемый, не codec E2E. Native comparison
использует parent/scratch scope evidence; Unicode не заменяется ASCII folding.
Пока поддержаны default directory layout и flat custom templates; остальные
layout явно блокируются уже в preview, не подменяются default. Общие collision
suffix/reorg/automatic recovery и assets acceptance этим не объявлены готовыми.
Полный gate ещё не прошёл: закрываются frontend lint и review regression для
cross-parent namespace collision. Native CI среза ещё не запускался.

Срез подготовки first-audio publication: coherent repeatable-read context
с metadata четырёх scopes, credits, template bindings и проверенным selected
source/probe; optional resolution position → track → recording → release,
где explicit-empty останавливает наследование. Добавлены unit/PG regressions
для projection, fresh source и stale inventory path. Native Unicode component
probe сравнивает реальные filesystem identities и выполняет owned cleanup,
включая отмену; это ещё не production namespace planner или admission witness.
ASCII-only comparison helper не подключается и удалён. Полный локальный
`task verify` прошёл перед удалением этого неиспользуемого helper; commit hooks
повторно проверяют итоговое дерево. First-audio preview/admission/API/UI
и native witness binding ещё не завершены.

Текущий user-facing срез добавляет coherent position publication
read model, server-owned saved-manual-lyrics admission, GET/POST Huma endpoints
и library release route с явным Save → Apply. Value/removal admission прошли
реальную PG проверку без original source; API/UI tests и полный `task verify`
прошли. UI lifecycle subscriptions/draft/readback gaps исправлены и покрыты
regression tests. Исправлена Windows native directory error classification
из CI worker-среза; native CI этого исправления ещё ожидается. First-audio HTTP admission,
automatic recovery и полный acceptance плана ещё не завершены.

В текущем delivery-срезе добавлены общий ID-only job args,
River publication worker и app registration с process-wide coordinator/boot ID.
Executor сам получает durable claim и guarded frozen inputs, использует existing
source hold и запускает existing generic или manual-FLAC lifecycle. Approved
target ancestors создаются через pinned native capabilities по frozen manifest;
при неопределённости claim удерживается, safe diagnostic не подменяет settlement.
Real-PG manual delivery и ordinary audio delivery в пустой root/nested target
прошли; audio fixture использует контролируемый command transport, не codec E2E.
Полный локальный `task verify` прошёл, review блокеров не нашло; native CI ещё
не проверен. HTTP admission, automatic recovery и остальные acceptance
gates плана этим не объявляются завершёнными.

В текущем refactor-срезе publication inputs сведены к одному
current envelope: marker 1, обязательная current quality policy, audio/rewrite
и optional manual-lyrics authority вместо V1/V2/V3 dispatch. Compatibility
constants/readers удалены; generic/private capability boundaries сохранены по
семантике. Manual commit parser больше не фабрикует ordinary witness без receipt:
общий validator получает исходные десять shared records. Прежние frozen inputs
не переписываются, миграции и локальная БД не сбрасываются. Полный локальный
`task verify` прошёл; review выявленный policy replay gap исправлен и покрыт
реальным committed-journal regression. Native CI ещё не проверен;
это не завершение publication worker/recovery/UI.

Текущее уточнение владельца снимает обязательный process-tree containment gate:
для команды достаточно дождаться основного процесса. Добавлен Linux/macOS
backend с cancellation и ограниченным ожиданием inherited output pipes;
Windows Job Object backend сохранён. Focused Unix tests прошли после исправления
test-helper stdout; окончательный полный gate ещё выполняется. Production worker
пока не подключён. Согласована консолидация unreleased input V1/V2/V3 в один
актуальный контракт без compatibility readers и без сброса локальной БД;
рефакторинг ещё не выполнен. Подробности решения — `docs/design/decisions.md`.

В текущем test-срезе добавлены реальные native/PG lost-installed-
ACK и post-commit cleanup-intent failure cases. Первый сохраняет G0/no receipt
при installed bytes и private borrow; второй сохраняет committed G1/receipt и
completion fence до cleanup, оба без local release. Review замечаний не нашло;
полный локальный `task verify` прошёл. Native CI этого среза ещё не проверен.
Recovery/resume этим не добавлены.

В текущем private lifecycle-срезе объединены retained admission
handoff, native atomic replacement, exact installed proof → completion-token
transfer, receipt commit и owned cleanup/settlement. Обычный private finish
после installed proof запрещён. Native/PG happy path и post-rename commit-error
retention и полный локальный `task verify` прошли; review блокеров не нашло.
Native CI этого среза ещё не проверен.
Installed-ACK ambiguity и cleanup-failure matrix ещё не покрыты V3 full-path
tests. Production worker/API, межпроцессные containment claims и recovery resume
не включены; Windows directory durability остаётся fail-closed до replacement.

В текущем persistence-срезе private V3 replacement recorder
проверяет canonical exposure prefix, все replacement bindings и full locked
CURRENT snapshot перед каждым append. Generic V3 replacement не разрешён.
Native-fixture PG tests моделируют только четыре DB journal transitions:
installed evidence не означает filesystem rename, fixture не коммитится.
Review выявило missing installed-mtime binding; исправление и regression
прошли полный локальный `task verify`. Финальное review блокеров не нашло;
native CI этого среза ещё не проверен.

В текущем service-срезе continuous private owner удерживает
work capability через exposure-admission ACK, выводя intent внутри из frozen
manifest и anchored existing target parent. Nested target/canonical-key case и
четвёртый-ACK success/error tests добавлены; полный локальный `task verify`
прошёл, review блокеров не нашло. Native CI этого среза ещё не проверен.
Root namespace сравнивается с pinned output handle, target name перепроверяется
после hashing; каталогов и replacement capability owner не создаёт. Admission
по-прежнему не разрешает filesystem replacement или recovery resume.

В текущем срезе добавлены private validated/exposing CURRENT
reader и post-validation checkpoint, требующий active scope-held capability.
Generic rewrite checks сохраняют preparing/staged границу. Native/PG tests
проверяют detached frozen input, stale guard и отсутствие mutation при чтении;
полный локальный `task verify` прошёл, review блокеров не нашло. Native CI
этого среза ещё не проверен. Reader не даёт resume/replacement authority;
continuous exposure admission owner пока не подключён.

В текущем persistence-срезе private manual FLAC exposure
admission принимает только canonical validated prefix и exact stored observation,
проверяя CURRENT G0 и namespace. Replay требует ровно один final exposure record;
generic V3 exposure отвергается и на replay. Добавлены native/PG DB-only tests;
полный локальный `task verify` прошёл, review блокеров не нашло. Native CI
этого среза ещё не проверен. Это admission, не filesystem exposure,
не непрерывный service handoff и не разрешение managed replacement/recovery.

В текущем service-срезе отдельный private validated owner
сохраняет точный work capability через outcome/staged/validated ACK. Между ACK
проверяются native output identity/hash и namespace без замены acknowledged
evidence; mutable repo evidence отделено deep clones. Outcome-only entry
сохраняет preparing-семантику. Добавлены continuous PG cases и fake ACK
error/stale/unknown/alias regressions; полный локальный `task verify` прошёл,
review блокеров не нашло. Native CI этого среза ещё не проверен.
Exposure, publication commit и production V3 execution не включены.

В текущем persistence-срезе добавлены guarded manual FLAC stage
transitions с typed ACK и exact stored-outcome observation. Intermediate grammar
сохраняет canonical preparation/rebuild/outcome prefix и не более двух ordered
stage records; replay заново проверяет frozen intent, CURRENT и namespace.
Native/PG tests дополнены DB advance/replay; это отдельно от service owner и не
доказательство непрерывного filesystem exclusion после его возврата. Полный
локальный `task verify` прошёл; review блокеров не нашло. Native CI этого среза
ещё не проверен. Generic V3 staging/exposure не включены.

В текущем срезе private FLAC owner поддерживает один verified
native front cover без реконструкции: actual cover SHA/size сопоставляются с
opaque snapshot, полные PICTURE-body и encoded audio сохраняются. Реальные PG
cases no-cover/value, covered/value и covered/removal прошли focused-проверку;
removal начинается с существующих native lyrics. Полный локальный `task verify`
прошёл; native CI этого среза ещё не проверен.
Multiple/alternate artwork остаются за private capability boundary, без выбора
или удаления картинок; generic V3 execution и production Apply не включены.

В текущем срезе concrete private FLAC owner объединяет native
metadata write, sealed observation и typed optional-outcome ACK. Scope хранит
точный issued capability и исключает commands/finalization через весь boundary;
неподтверждённый ACK сохраняет quarantine и borrow. Native positive/failure tests
и реальный PostgreSQL/native prepare → outcome ACK test прошли; подготовка и
outcome journal записаны реальными методами, admission fixture seeded отдельно.
Полный локальный `task verify` прошёл. Private composed path пока допускает
только verified absence cover; это временная capability, не продуктовая политика.
Windows CI `0a8b793` выявил sync через read-only handle; добавлен anchored
identity-bound writable sync без пропуска durability gate. Финальное review не
нашло блокеров; native CI исправления ещё ожидается. Это не production worker, не exposure и
не разрешение generic V3 execution.

В текущем срезе добавлен sealed native FLAC observation builder:
initial-copy proof и guarded frozen inputs связывают private authority; observation
удерживает execution fence, проверяет native output identity/hash, mandatory tags
и exhaustive artwork/audio equivalence до переноса frozen CURRENT audio facts.
Ownership берётся от actual output, LYRICS остаются фактическим readback для
optional classifier. Positive native и mandatory/audio rejection focused tests
и полный локальный `task verify` прошли; review не нашло блокеров. Native CI
этого среза ещё не проверен. Wrapper integration,
durable outcome orchestration и V3 execution этим срезом не включены.

В текущем срезе добавлен private initial-output copy для явно
подготовленного V3 FLAC stage: exclusive fixed-name creation, frozen SHA checks
input/CURRENT/output, повторная проверка native output entry и pinned synchronous
reservation с command-launch/finalization fence. Новых journal records, recovery
authority или V3 execution wiring нет. Native output-substitution regression,
reservation/finalization tests и полный локальный `task verify` прошли;
native CI этого среза ещё не проверен. Durable candidate completion этим helper
не объявляется.

Текущий срез добавляет FLAC artwork equivalence/cover facts и metadata write,
который проверяет все mandatory tags, оставляя только LYRICS для optional
readback classifier. Verified absence artwork поддержана отдельно от invalid
snapshot. Private preparation reader повторно проверяет live delivery, CURRENT
generation/ownership и namespace; generic V3 execution остаётся запрещённым.
PostgreSQL positive/refusal tests и полный локальный `task verify` прошли;
независимое review не нашло блокеров. Native CI этого среза ещё не проверен.
Preparing-state fixture моделирует состояние в test transaction,
не доказывает runtime lifecycle. Незавершённый private runner удалён: initial
candidate copy и callback-free final observation ещё не реализованы.

Реализован private FLAC artwork verification: bounded structural audit отвергает
multiple/non-front/alternate artwork до mutation; payload/descriptors читаются
TagLib, полные native PICTURE-body и encoded-audio digests проверяются после
tag-only write. Snapshot связывается с native identity до записи; identity
сменившего файл TagLib после записи не считается fidelity failure. Full picture
restoration пока unsupported: pinned TagLib API не предоставляет все numeric
PICTURE fields для реконструкции. Это временная private capability, не новая
продуктовая политика выбора/удаления covers. Полный локальный `task verify`
прошёл; native CI этого среза ещё не проверен.

В текущем срезе required assets восстанавливаются после каждой
успешной clearing metadata write, включая initial lyrics attempt; callback
получает expected tags, чтобы не стереть выбранный LRC. Rebuild использует
исходный preparation owner. Раздельные real-TagLib tests покрывают successful
lyrics+cover и mismatch fallback с сохранением cover/encoded audio. Review не
нашло блокеров; полный локальный `task verify` прошёл. Native CI ещё не проверен.
Это не доказательство
exhaustive artwork preservation; V3 execution не включён.

В текущем срезе V3 receipt-bearing journals поддержаны в
replacement cleanup/settlement: canonical boundary определяется отдельно для
V2/V3, stored receipt сверяется в каждой транзакции до idempotent ACK.
Новый desired revision не переименовывает receipt старого admitted revision.
Независимое review не нашло блокеров; полный локальный `task verify` прошёл.
Native CI этого среза ещё не проверен.
V3 filesystem execution и production Apply не включены.

Добавлен атомарный V3 receipt-bearing replacement commit: canonical exact/rebuild
prefix связывает frozen R, confirmed G0, published G1 и actual outcome; current,
receipt, witness и SQL NULL frozen inputs записываются в одной транзакции.
Replay проверяет existing receipt без repair/refresh. PostgreSQL tests покрывают
value, removal, оба fallback outcomes и rollback при receipt/operation failures.
Полный локальный `task verify` прошёл; native CI ещё не проверен. V3 execution,
cleanup/settlement integration и production Apply остаются выключенными.

Реализованы guarded optional-lyrics outcome journal и private service adapter:
typed ACK различает stale/replay/new record; canonical preparation/rebuild,
actual namespace, copied-input size/hash и independent final observations
проверяются до записи. Frozen revision R не переименовывается в R+1 после нового
save. V3 execution остаётся запрещённым. Receipt-bearing replacement witness,
атомарная запись receipt в commit и production Apply пока не реализованы.
Полный локальный `task verify` outcome-среза прошёл; native CI ещё не проверен.

Добавлен durable manual-lyrics receipt storage/read-model: applicability
ограничена active epoch, selected desired revision и published generation;
fulfilled требует строгих actual metadata/assets, warning остаётся diagnostic,
а не самостоятельным pending-флагом. Production receipt writer и canonical
commit integration ещё отсутствуют. PostgreSQL constraints, read projection и
populated rollback refusal проверены; полный локальный `task verify` прошёл,
native CI ещё не проверен. Native rebuild test теперь наблюдает успешные
unlink/exclusive-create вместо предположения о lifetime-уникальности file ID.

Реализованы isolated optional-lyrics helper и native candidate rebuild: исходный
live reservation удерживается через write/readback/rebuild; rebuild journal
предшествует identity-checked unlink и exclusive byte-copy из immutable input.
Opaque writer errors остаются fatal; mismatch допускает fallback лишь с
независимой required audio/asset validation и restoration authority. Тест FLAC
проверяет encoded-audio digest, mandatory tags до восстановления cover и exact
lyrics absence после fallback. Полный локальный `task verify` прошёл; native CI
ещё не проверен. V3 execution остаётся
запрещённым; durable warning/retry state и runtime integration ещё отсутствуют.

Реализован unregistered V3 manual-lyrics admission: locked desired revision,
release membership и confirmed current generation ограждают manual LRC/removal;
mandatory tags берутся только из current publication. Generic V3 admission
запрещён, существующие execution paths явно отвергают V3 до tools/filesystem
mutation. Это не executable Apply: optional-writer isolation, durable warning/
retry status, route/UI и runtime wiring ещё отсутствуют. Полный локальный
`task verify` прошёл; native CI этого среза ещё не проверен.

В текущем срезе связаны staging → sealed replacement → commit →
owned backup cleanup → terminal settlement в исходном live execution scope.
Rewrite settlement не требует original source hold; отсутствие источников не
снимает проверки backup/current/namespace. Native Unix runner и PostgreSQL
settlement проверены focused-тестами; полный локальный `task verify` прошёл,
native CI ещё не проверен. Windows
replacement по-прежнему fail-closed до подтверждения directory durability;
production worker не зарегистрирован, cross-boot recovery не реализован.

Реализован атомарный replacement commit: проверяется полный canonical prefix,
actual namespace, expected current projection, staged observation и отдельная
identity приватного backup. Commit сохраняет source-free current, потребляет
frozen inputs через SQL NULL и удерживает claims/holds; replay требует exact
current witness. Полный локальный `task verify` прошёл; native CI ещё не проверен.
Replacement cleanup,
settlement и связь exposure → commit в runtime пока не завершены.

Следующий срез — guarded same-path rewrite exposure: проверенный
private backup остаётся доступным до единственного native atomic replacement;
ordered replacement journal связывает actual namespace, current generation и
validated candidate. Unix использует file/directory sync barriers; Windows
exposure пока fail-closed из-за неподтверждённой directory durability capability.
Replacement commit, cleanup/settlement и workflow wiring ещё отсутствуют;
после exposure scope остаётся fenced. Полный локальный `task verify` среза прошёл;
native CI для него ещё не проверен.

Реализован следующий срез: завершение first-absent публикации
в исходном live execution scope — sealed synchronous reservation, anchored
удаление только собственного private input и пустого attempt directory,
cleanup journal и атомарный terminal settlement. Полный локальный `task verify`
прошёл; native CI ещё не проверен. Это не recovery takeover, optional source
cleanup или разрешение регистрации worker. Сквозной тест real preparer → real
persistence и полное покрытие commit/cleanup panic boundaries ещё отсутствуют.

Дополнение (startup barrier): `app.Run` проверяет read-only
repeatable-read snapshot публикаций до platform initialization, output-reset
recovery, consumers и HTTP. Retained execution claims независимо от их состояния
и несогласованные attempts блокируют запуск; never-started queued attempts
допускаются только с проверенными River delivery и namespace reservations.
Это не interprocess exclusion, recovery execution или settlement и не разрешение
на регистрацию publication worker. Полный локальный `task verify` текущего
среза прошёл. CI `edb5fc9` (run `39112796255`) прошёл на Linux amd64/arm64,
macOS x64 и Windows x64, но завершился ошибкой macOS arm64 в независимом
MusicBrainz cooldown concurrency test; container publish пропущен. Исправление
тестового измерения через `testing/synctest` ещё проверяется.

После исторической отметки ниже добавлен узкий filesystem publication slice:

- При первом отсутствии target проверенный staged file публикуется через
  no-replace; ownership journal устанавливается после публикации. Первый
  publication commit выполняется атомарно и сохраняет `NULL` source provenance
  по frozen snapshot.
- Execution scope закрывается с quiescence barrier; удерживаемые input
  descriptors освобождаются только после установленного завершения использующих
  их команд. Неизвестная quiescence не трактуется как успех.
- Эти изменения вошли в `c32e10b`, `cd5eec1` и `0ea97ea`. Для каждого из этих
  коммитов `task verify` и локальные hooks действительно прошли.
- V2 typed evidence подтверждает нормализацию первого cover и ограничения
  evidence: topology covers пока отвергаются как non-audio; сохранённый optional
  sample rate не является доказательством bit-perfect результата.

Это не завершает реализацию публикации. Recovery execution, private-artifact
cleanup и settlement, replacement/upgrade/rewrite exposure, runtime interprocess
startup exclusion, Linux/macOS containment и Windows native CI остаются
непроверенными (Windows CI для этой серии ещё не запушен). Acquisition assets,
UI и automatic triggers также не образуют завершённый end-to-end путь; план 12
остаётся незавершённым. Это progress evidence, а не изменение продуктовых
решений и не отметка завершения P05/T07 или иных checklist items.

### Историческая отметка прогресса — 2026-10-17 (после `1c3bb62`)

Реализованы подготовительные foundation-срезы: атомарный admission публикации в
namespace с durable claims и River jobs, содержащими только operation ID;
durable source holds и защита мутаций источника согласно A.17; typed execution
inputs с probe-verified selected facts; подготовка переписывает только
ownership-verified artifact, не оригинальный source; проекция согласованных
metadata в tags и staged writer/preparers. Добавлены exclusive stage journal,
supervised execution scopes с удержанием до доказанного quiescence,
descriptor-bound probing, разрешение инструментов из frozen managed tools и
ограничения demuxer/protocol для publication inputs. Это foundation, а не
завершённые exposure/recovery или end-to-end publication slices.

Windows job backend пока **UNREGISTERED**; проверка membership процесса в job —
не arbitrary broker containment и не полный sandbox. Native Windows поведение
не проверено, CI для этой серии ещё не запушен. Также ещё не валидированы
sample-rate и сохранение аудиобайтов как фактические характеристики результата.
После settle не сохраняется история исходного аудиоисточника; это не означает,
что сохранение аудиобайтов текущей публикации уже доказано.

Остаются runtime interprocess exclusion, доказательство поведения при потере БД
на старте, Linux/macOS containment, exposure journal/recovery/settlement,
wiring cover/lyrics assets, automatic reconciliation и cleanup, а также UI/API
end-to-end, Navidrome/tag mapping и distribution/licensing. Поэтому план 12 не
завершён и не переносится в `done/`; owner contract и отложенный scope этим
статусом не меняются.

Последняя указанная полная проверка `task verify` прошла; изменения внесены
отдельными коммитами с локальными hooks, но пока не pushed. Ключевой диапазон
реализации: `d649800`–`1c3bb62` (последний — demuxer/protocol policy).

**Статус: DRAFT.** Подготовлена предметная декомпозиция следующей основной
области. Это не разрешение на реализацию: перед implementation необходимо
получить явные решения владельца по открытым продуктовым пунктам D01–D05 и
зафиксировать их в этом плане. Технические варианты ниже — предложения для
review, а не утверждённый контракт. До этого план не готов к реализации.

## 1. Цель и границы

Сделать путь от существующей локальной медиатеки до проверяемого управляемого
аудиоартефакта сквозным: открыть релиз и его позиции, выбрать применимые
источники, увидеть предварительный результат, явно запустить публикацию и
прочитать фактический созданный файл. Состояние в БД само по себе не доказывает,
что публикация существует или что её теги/аудио содержат ожидаемое.

План покрывает ручную, в том числе частичную, публикацию и утверждённый
автоматический сценарий полного уверенного релиза. Автоматический выбор источника
зависит от согласования политики качества. Ни одна неподтверждённая политика
не должна быть закодирована как default. Выделение ручного-only первого релиза
поставки — предложение о последовательности, требующее отдельного согласования,
а не сокращение целевого scope.

### Не входит

- Пересмотр source inventory, matching, provider acquisition или Operations как
  самостоятельной следующей области. План 11 завершён; Operations не является
  следующим этапом.
- Новая модель локальной медиатеки, изменение matching/unlink semantics или
  автоматическое удаление и повторное связывание `track_source`.
- Расписания, периодический scan/retry либо массовая миграция старых сущностей.
  Конкретные triggers автоматической публикации требуют проработки D04;
  сам утверждённый автоматический сценарий этим планом не отменяется.
- Редактирование исходных файлов. Source roots по умолчанию read-only; отдельная
  настройка удаления исходника после успешной публикации уже предусмотрена
  действующими требованиями, но её поведение и охват требуют решения D05.
- Закрепление физического layout, naming convention, audio codec/preset, лимитов,
  digest policy, новых зависимостей или env-переменных без обоснованного решения.

## 2. Источники требований и подтверждённые ограничения

При расхождении применять актуальные `docs/design/decisions.md`,
`requirements.md`, `data-model.md` и правила архитектуры, а не более строгую
реализацию, прототип или исторический аудит. Источники для этой проработки:

- [Решения владельца: источники и публикации](../../design/decisions.md#источники-и-публикации)
  и [контракт инвентаря/output](../../design/decisions.md#текущий-контракт-инвентаря-анализа-и-output).
- [Требования](../../design/requirements.md#источники-analysis-и-output),
  [логическая модель](../../design/data-model.md) и
  [границы слоёв](../../design/repository-architecture.md).
- [План 10](../done/10-staged-source-analysis.md): в приложении A сохранены
  решения по source freshness, output reset, сохранению локальных сущностей и
  источников. Это свидетельство согласованного контракта, но не текущая
  реализация публикации.
- [План 11](../done/11-incoming-grouping-and-automatic-matching.md): завершённое
  incoming/matching поведение. Релиз может быть локальным и не иметь provider
  links; пустые ручные значения допустимы; matching не выбирает качество и не
  публикует файлы.

Уже подтверждено и не открывается этим планом повторно:

1. Сервис работает с путями файловой системы сервера. Публикация — управляемая
   сервисом копия выбранного аудиоисточника с метаданными релиза/позиции,
   artwork и lyrics. Исходники по умолчанию не изменяются.
2. Локальные release/track самостоятельны и допускают публикацию без provider
   match. Метаданные тега для конкретной позиции принадлежат позиции релиза,
   даже если один `media_variant` используется в нескольких позициях.
3. Для каждой позиции допускается одна активная опубликованная копия; один
   исходный файл может быть выбран для разных позиций/релизов. Дубли публикации
   одной позиции не создаются.
   Целевой результат — извлечённое аудио выбранного источника, при необходимости
   помещённое в выбранный контейнер и снабжённое согласованными новыми тегами,
   с заменой опубликованного артефакта как одного целого. Это утверждает
   наблюдаемый результат, но не способ staging, атомарности конкретной FS
   операции, имена временных файлов или crash-recovery protocol.
4. Автоматическая публикация не заменяет текущую копию источником того же или
   худшего качества. Оператор может явно выбрать любой подходящий источник.
   Формула качества требует согласования; scores разных policy versions напрямую
   не сравниваются — оба варианта оцениваются по текущей policy.
5. Свежесть источника на продуктовом уровне определяется совпадением
   наблюдаемых `size` и `mtime`. Более строгие filesystem/race guards могут быть
   техническими средствами, но не становятся новыми продуктовыми блокерами.
6. При смене output запрещено изменение во время running/claimed execution;
   queued work и все DB references на прежний output сбрасываются транзакционно.
   Старые файлы остаются на диске unmanaged; локальные entities, source links,
   inventory и analyses сохраняются; автоматической повторной публикации нет.
   Publication execution обязан быть включён в running/claimed fencing и reset.
7. История завершённых попыток как продуктовая функция не требуется. Модель
   описывает актуальную публикацию и незавершённые попытки, а не архив.
8. Настройка `publication_format` уже существует и валидирует значения `source`
   и `mka`. Это существующее состояние настройки, а не полное решение о
   контейнере, кодеке, пересэмплировании или преобразовании тегов.

## 3. Проверенное текущее состояние кода

Состояние сверено при подготовке плана по migrations, settings, persistence,
integrations и `backend/internal/publication/`:

- Пакет `backend/internal/publication/` сейчас содержит только `.gitkeep`; его
  рабочей логики публикации нет.
- В последовательности baseline migrations `20261015000001`–
  `20261015000005` заведены application core, source inventory/analysis,
  provider cache, local library и incoming matching. Publication tables и
  публикационных API/jobs/UI в текущей реализации нет.
- Действующие сущности включают `local_release`, `release_track`, `local_track`,
  `local_recording`, `track_source`, `media_variant` и provider links.
  Artwork candidates и lyrics selections описаны целевой моделью, но их runtime
  таблицы/API ещё не поставлены. Это существующая основа, а не утверждение, что
  её схема полностью покрывает D01–D05.
- В Settings уже есть `PublicationFormat` и методы чтения/сохранения
  `publication_format` со значениями `source`/`mka`. Настройка не доказывает,
  что любой выходной файл можно безопасно получить выбранным способом.
- В проекте имеется TagLib wrapper для **чтения** source tags/properties; в
  runtime он не является согласованным/готовым writer-контрактом. Добавление
  writer или новой библиотеки требует технической и лицензионной проверки;
  текущую зависимость нельзя молча считать решённой для записи/распространения.
- Output reset уже использует durable coordinator/journal. Планирование должно
  встроить publication admission/claims и ссылки артефактов в существующие
  механизмы, а не проектировать обходной reset.

Перед implementation повторно проверить текущие имена таблиц, APIs, composition
root и fences: migrations могут быть обновлены независимо от этого документа.

## 4. Реестр открытых решений владельца (блокирует зависящие этапы)

Ниже — конкретные продуктовые вопросы. Варианты приведены как предмет выбора,
не как рекомендованный или согласованный default. Если решение влияет на другой
пункт, фиксировать связь и итог совместно; не заполнять пропуск кодом.

### D01 — идентичность output, структура и коллизии

- Целевая модель уже разделяет каталог публикации релиза и файлы отдельных
  позиций. Уточнить их физическую раскладку и адресацию, не отменяя это разделение.
- Какие части пути формируются из локальных artist/release/track полей; как
  обрабатываются пустые значения, разделители, недопустимые имена, длина,
  регистр/Unicode semantics и переименование сущности?
- При существующем постороннем файле/каталоге по рассчитанному адресу: показать
  коллизию, разрешить явное подтверждение замены, выбрать альтернативу или иной
  вариант? Как различить управляемый artifact и чужой файл после restart?
- Какая гарантия уникальности нужна с учётом возможностей уже проверенного
  filesystem semantics probe? Не предполагать, что PostgreSQL строковое сравнение
  моделирует семантику FS.

### D02 — выбираемый source, audio stream и сегмент

- Как трактовать отсутствие `track_source`, недоступную/удалённую location,
  неуспешный/отсутствующий probe и несовпадение наблюдаемых size/mtime?
- Как оператор выбирает одну из нескольких связей `track_source`; как UI
  различает source path, location, shared `media_variant` и release position?
- Какой audio stream использовать при нуле, одном или нескольких audio streams?
  Нужен ли ручной stream selector; можно ли опубликовать source с неизвестной
  структурой или публикация блокируется?
- Как обрабатывать CUE/manual segment boundaries: точность/округление, границы,
  длительность, пропущенную/устаревшую разметку, несколько позиций из одного
  source? Не вводить авторазделение большого файла без отдельного решения.
- Как обрабатывать неподдерживаемые входные форматы/stream layouts? Целевой
  результат — извлечённое audio; отсутствие согласованной поддержки должно
  приводить к явной ошибке, а не молчаливому выбору другого stream. Конкретный
  форматный охват требует подтверждения tool capability, а не предположения.

### D03 — режим `source`/`mka`, теги и значения

- Для `source`: как именно извлечённый audio представляется в source-form output;
  какие container/codec свойства сохраняются и какие изменения контейнера
  разрешены? Как объяснить несовместимость выбранного metadata mapping с форматом?
- Для `mka` сохраняется утверждённый remux без перекодирования. Проверить
  ограничения Matroska Audio на выбранные streams/codecs и согласовать поведение
  при несовместимости; не вводить транскодирование как fallback.
- Состав полей и mapping для artist credits, album artist, release/track title,
  номера/носителя, даты, MBID, genre и других полей. Как различать отсутствие,
  пустую строку, явный пустой override, несколько значений и порядок значений?
  Не заполнять отсутствующие значения source tags или provider data молча.
- Приоритет локального поля, явного override, выбранного provider значения и
  source tag; правила для удалённых/пустых overrides; представление multi-value
  tags в target container. Согласовать отдельно readback/preview и фактическую
  запись, чтобы интерфейс не показывал выдуманное содержимое.
- Требуется ли сохранение нераспознанных/пользовательских source tags, artwork
  embedded tags и технических stream properties? Не обещать lossless metadata
  preservation без подтверждённой реализации.

### D04 — качество, выбор и автоматический режим

- Какая проверяемая политика определяет пригодность источника и сравнивает
  варианты: какие измерения, веса/порядок, missing values, пороги, равенство и
  несовместимость policy versions? Текущие поля quality в conceptual model не
  задают формулу.
- Частичная ручная публикация уже разрешена: согласовать точный preview/API и
  обозначение пропусков, не превращая полное покрытие релиза в обязательное условие.
- Полный уверенно сопоставленный релиз предусматривает автоматическую
  публикацию. Уточнить конкретный trigger, orchestration и реакцию на
  ties/unknown quality; не пересматривать разрешение automatic flow.
- Равное/худшее качество не запускает автоматическую замену. Уточнить поведение
  при недоступном source и способ переоценки обоих вариантов по текущей policy.
  Явный ручной выбор любого подходящего источника сохраняется; определить
  интерфейс предупреждения/подтверждения, не запрещая ручной downgrade.
- До ответа D04 автоматический selector заблокирован; никаких придуманных
  quality score/default thresholds. Возможность отдельной ручной поставки
  согласовать явно, не объявлять весь план завершённым без automatic flow.

### D05 — assets, lifecycle, ошибки и изменения

- Включение artwork и lyrics входит в обязательную публикацию или допускаются
  настройки/отсутствие отдельных assets? Где берётся artwork (выбранный
  release candidate, встроенная картинка, обе возможности), как обрабатывается
  неизвестный MIME/ошибка? Lyrics могут быть несколькими языками/типами; что
  публикуется и в каком представлении?
- Какие assets создаются в том же atomic intent, а какие могут публиковаться
  позже? Разрешена ли публикация audio, если artwork/lyrics отсутствуют или
  передача asset завершилась ошибкой? Нельзя молча трактовать обещание «включены»
  как «попытались, ошибка скрыта».
- При обновлении tags/source/format/path: нужна ли немедленная публикация новой
  версии, явно запускаемая замена или сохранение старого output? При ошибке
  остаётся старый корректный файл или допустимо состояние без опубликованной
  копии? Что означает delete/reorganize и кто инициирует его?
- Source cleanup после подтверждённого DB-результата выполняется только при
  актуальном opt-in; перед началом проверяются связанные публикации/validation и
  queued/running consumers. Уже завершённая job при redelivery не делает
  файловую работу. Остальные правила source cleanup зафиксированы отдельно в
  решениях владельца и не меняются этим упрощением.
- Удаление source root/location не должно стирать локальную сущность или
  source bytes. Нужно определить effect на активную публикацию и retry; не
  расширять утверждённый output reset до удаления физических старых файлов.
- Сбой asset/audio/DB commit, partial success по релизу и retry/restart:
  операторский статус и возможность повтора; нужны ли допустимые частичные
  результаты и как они отражаются без выдачи успеха за полный успех?

## 5. Технический дизайн после решений

Эти задачи уточняют физическую реализацию после закрытия соответствующего D-gate.
Они не требуют owner approval сами по себе, пока не меняют продуктовые правила;
схема, границы и детали должны пройти независимое техническое review.

### D06 — data lifecycle и публикационный read model

Описать актуальный persisted state, attempt state и идентификаторы артефактов:
release-level metadata/assets, position-level source/quality/output, отсутствие
или наличие управляемой копии, незавершённые операции и причины ошибок. Не
хранить завершённую историю как пользовательский feature. Сопоставить логическую
модель `release_publication`, `track_publication` и attempts с действующими
`local_release`, `release_track`, `local_track`, `track_source`, variant и будущими
artwork/lyrics сущностями. Уточнить уникальные ключи и поведение cascade так, чтобы DB cascade не
считался удалением файлов. Зафиксировать, какие данные являются фактическим
readback опубликованного файла, а какие являются только намерением/metadata
snapshot.

### D07 — operation, fences и replacement

Durable River job несёт только operation ID; enqueue атомарен с admission через
`River.InsertTx`, worker всегда перечитывает полный актуальный input. Общий
coordinator сериализует destination/позицию и конкурирующие операции сохранения,
reset и delete, без удержания SQL transaction во время подготовки/ffmpeg.
Проверяются актуальность ручного выбора и право владения target. Полный кандидат
подготавливается приватно на той же FS рядом с destination, валидируется/readback
проверяется и публикуется одной native atomic replace. Затем одна DB transaction
фиксирует actual projection, успешную operation, освобождение holds и consumed
attempt. При сбое до commit подготовка повторяется полностью; повторный replace
допустим. Нет requirement на exactly-once, шаговые journal/ACK, backup/rollback,
resume или boot recovery. Retryable error возвращается River для повторной
доставки и не блокирует job на первой ошибке; terminal failure или исчерпание
retry дают видимую ошибку и ручной retry, а не вечный running. Отмена ожидает
основной процесс и собственные callbacks, но не требует process-tree proof.

### D08 — output namespace и staging

Private temporary candidate располагается рядом с фактическим destination на той
же файловой системе и заменяет его native atomic replace. Delete-then-copy fallback
запрещён; универсальная power-loss durability не обещается, directory sync не
является условием атомарности. Существующие логические области `analysis`,
`publication`, `checks`, `media` не задают закреплённые физические имена/иерархию.
Технический private layout не становится настройкой или пользовательским
контрактом. Не вводить произвольную quota/лимит размера.

### D09 — реальные tools и tag writer

Проверить актуальный FFmpeg/ffprobe lifecycle и поддержку extraction/remux/tag
writing на согласованных форматах; отдельно проверить target platform matrix,
лицензии, packaging/notices и замену/добавление writer library. TagLib в коде
используется для чтения, а не как одобренный writer. Нельзя реализовать preview,
который утверждает успешную запись полей, основываясь только на входной map.
Тесты должны открывать готовый файл независимым readback путём и сверять доступные
поля/stream properties. Выбор инструмента не меняет продуктовый контракт и не
должен расширять зависимости до завершения review.

### D10 — API, UI и operation feedback

Зафиксировать Huma request/response для небольшого release detail read model,
preview/validation, start/status/readback и явных действий замены/retry, только
после решений D01–D05. React использует Orval generated API; generated code не
править вручную. SSE остается wake-up channel: клиент перечитывает snapshot.
UI показывает выбранную позицию/source/формат и фактический путь/статус;
не скрывает пропуски, warning/error assets или то, что теги ещё не проверены.
Не превращать в обязательный prerequisite полный Library screen/всю навигацию.

## 6. Декомпозиция реализации после согласования

Срезы небольшие и допускают отдельный review. Все новые решения, меняющие
поведение, сначала возвращаются владельцу; технический дизайн не является
разрешением заполнить продуктовые пробелы.

| Slice | Результат | Зависимости / критерий приёмки |
| --- | --- | --- |
| P01 — контракт и минимальный read model | Закреплённые D01–D05; диаграмма данных/lifecycle; API shapes и ошибки; технические D06–D10; migration/reset plan. Минимальное чтение выбранного локального релиза и его позиций, без требования строить полный экран медиатеки. | Все нужные owner decisions записаны; независимое review дизайна завершено; подтверждены реальные schema names и отсутствие существующей publication реализации. |
| P02 — схема и repository | Additive migration для текущего publication state, незавершённой попытки/intent и owned artifact refs согласно P01; persistence чтение/запись в транзакциях; down migration безопасна для применённого прототипа. | P01. Constraints проверяют уникальность активной публикации, согласованность состояния и ссылок; cascade не удаляет filesystem artifact. Чистая migration up/down integration coverage. |
| P03 — безопасный ручной planner/preview | Чистое планирование preview из локальных release/track/source links/overrides и согласованного формата; до старта показывает конкретные позиции, source/segment, metadata outcome, target collision/validation. Planner versioned или имеет эквивалентную freshness guard по согласованному контракту. | P01–P02. Preview не меняет исходники, DB publication state или output files; stale preview не принимает молча новые inputs; empty/unknown/conflicting values и collision имеют проверенные outcomes. |
| P04 — интеграция writer и локальный staged output | Извлечь/подготовить согласованный audio stream/segment; скопировать stream или преобразовать только согласно D02/D03; записать agreed tags; сформировать temporary artifact под managed ownership и проверить его реальным readback. | P01, P03, D09. Source не меняется; до atomic replace существующий output остаётся нетронутым; после replace сбой до DB commit разрешается полным повтором. Source size/mtime проверяется продуктовым freshness rule; покрыть неподдерживаемый stream/format и ошибку инструмента. |
| P05 — atomic replace, результат и reset | Fresh whole-file prepare/readback, native same-FS atomic replace, затем одна DB result transaction и terminal operation. Общий coordinator/fences охватывают конкурирующие save/reset/delete; enqueue атомарен через `River.InsertTx`. Failure до DB commit повторяет подготовку целиком; recovery journal/protocol не добавляется. | P02–P04 и D07–D08. Target ownership/stale manual selection проверены; no delete-copy fallback. Reset не пересекает active publication, queued work и DB refs сбрасываются транзакционно, физические старые файлы остаются unmanaged. |
| P06 — ручной API и truthful UI | Показ release/track/source summary, preview, explicit start, operation snapshot, retry допустимой ошибки и реальный readback состояния. Частичные outcomes отражены согласно D05. | P03–P05 и D10. Huma/OpenAPI → `task generate`; UI без фиктивных значений, с доступными validation/error states; действие оператора явное; no hidden auto publish. |
| P07 — assets и их prerequisites | Спроектировать и реализовать отсутствующие persistence/acquisition/selection для artwork/lyrics в согласованном D05 объёме, затем resolve, staging, publication status и failure reporting; при надобности отдельные asset jobs/refs. | D05 и P05; отсутствие опционального asset не маскируется под успешное обязательное включение. Вынос assets в отдельную поставку требует явного согласования scope. |
| P08 — automatic quality policy и запуск | Чистый, версионированный selector для candidate sources; automatic flow полного уверенного релиза на согласованном trigger/scope. | Согласована формула D04, P01–P06. Тесты доказывают согласованное tie/unknown behavior, запрет автоматической замены равным/худшим качеством и дубликатов. Не выбирать winner произвольно. |
| P09 — regression/integration acceptance | PostgreSQL/River integration, migration tests, managed filesystem, writer/readback, crash boundaries, reset/save/delete interleavings, UI/API scenarios и native atomic-replace matrix. | Реализационные slices завершены. Изолированные runtime tests подтверждают фактический файл, не только DB snapshots. |
| P10 — independent review и завершение | Review всего контракта/реализации, исправление замечаний, runtime evidence, актуализация статуса и перенос плана в `done/` после принятой проверки. | P01–P09 по принятому scope. Полный `task verify`; ручные evidence с revision/platform/limitations. Не утверждать реализацию по одному green unit suite. |

Порядок P04/P05 может уточниться после D07/D09, но P06 не должен обходить
транзакционные admission/fencing из P05. P07/P08 заблокированы соответствующими
открытыми решениями, а не исключены из целевого scope. Их нельзя реализовать
через выдуманный default или молча вычеркнуть. Уменьшение первой поставки и любое
изменение пользовательского scope согласовать отдельно.

P05 также включает River worker: args содержат только operation ID, intent и
актуальные execution inputs перечитываются по утверждённому контракту; admission
и enqueue выполняются в одной Bun-транзакции через `River.InsertTx`. Повторная
delivery и restart не создают вторую публикацию. Worker вызывает service;
пакет `publication` владеет файловыми операциями, persistence — БД.

## 7. Обязательная проверочная матрица

Implementation slices должны добавлять проверки рядом с каждым контрактом; P09
сводит их в end-to-end evidence. Fixtures покрывают локальный-only релиз,
provider-linked релиз, допустимые пустые ручные overrides, несколько позиций с
общим variant, разные выбранные sources и отсутствие `track_source`.

### Планирование, чтение и формат

- Release detail не смешивает position metadata с тегами общего `media_variant`;
  preview соответствует выбранному source/segment/format и показывает неизвестные
  или пустые значения различимо.
- Ноль/один/несколько audio streams; невалидная, пустая, граничная и устаревшая
  segment selection; source location отсутствует/недоступна; size или mtime
  изменились относительно preview.
- `source` и `mka` проверяются на согласованных реальных форматах. Проверяются
  stream codec/format, отсутствие случайного re-encode, где он запрещён, и
  readback фактически записанных metadata; неподдерживаемые файлы fail явно.
- Artwork и lyrics: отсутствие, multi-value/language selection, успешное чтение,
  ошибка получения/копирования/проверки и D05-определённая partiality.

### Filesystem, attempts и сбои

- Target отсутствует; target — managed prior output; target — чужой файл;
  collision case/Unicode normalization; неожиданная directory/file; недостаток
  прав; ENOSPC; отмена и недоступный/удалённый source; ffmpeg/writer missing,
  stale version или ненулевой exit; некорректный/неполный staged output.
- Failure-before-DB-commit, включая crash после atomic replace и до DB commit:
  повторная delivery полностью заново готовит/проверяет и заменяет target; нет
  требования откатить файл, продолжить шаг или доказать exactly-once. Crash после
  DB commit и до завершения River job приводит к redelivery без повторной файловой
  работы.
- При lyrics/source-free rewrite повторное применение в соответствии с действием
  устанавливает, обновляет или удаляет lyrics, сохраняя audio bytes и artwork и
  не выполняя lossy re-encode; проверить add/update/removal и readback для
  каждого действия.
- Сериализация конкурентных writes и stale manual selection относительно save,
  reset и delete; другой owner не перезаписывает target. SQL transaction не
  удерживается во время ffmpeg.
- Отмена и terminal retry exhaustion дают видимую ошибку/ручной retry и не
  запускают cleanup; проверяется ожидание основного процесса и callbacks, не
  process-tree containment.
- Source cleanup выключен по умолчанию: opt-in выключен к моменту исполнения,
  consumer protection, и redelivery после успешного DB результата при уже
  отсутствующем source не делает файловой работы. Cross-path cleanup старого
  managed output после нового DB результата отдельно проверяется как обычный
  warning при ошибке.
- Native atomic replace проверяется на поддерживаемых платформах; private temp
  должен быть на той же FS, delete-copy fallback отсутствует. Не требовать
  directory-sync gate или универсальной power-loss durability.
- Две параллельные попытки одной позиции и конфликт разных tracks на target;
  проигравший не перезаписывает победителя и не оставляет неконсистентные refs.

### Reset, связи и статус

- Output reset против queued, running, claimed publication; против claim/reset
  гонки; reset prepare/DB commit/recovery. Running/claimed операция запрещает
  смену output; queued work и references инвалидируются в одном согласованном
  reset, старые physical files остаются unmanaged.
- Reset сохраняет local entities, source links, inventory и analyses, не
  ставит автоматическую публикацию; root/location deletion и matching changes не
  удаляют source bytes и не симулируют publication success.
- DB failure/rollback не оставляет UI green state или broken active ref; отдельно
  покрыть crash после replace до DB commit и после DB commit до River completion.
  API/UI operation snapshot и перечитанный файл согласованы по agreed semantics;
  SSE wakeup не является data source.
- Повторная публикация сохраняет ровно один активный экземпляр на track;
  asset error, partial release и manual replacement отображают раздельно то, что
  действительно committed.

## 8. Acceptance gates и правила реализации

1. **До начала P02:** D01–D05 отвечены владельцем; P01 reviewed; выбранный output
   layout и writer имеют техническое подтверждение; миграционный/reset lifecycle
   согласован с текущими fences. Открытые решения явно помечены deferred, а
   зависящий функционал исключён из scope, не реализован по умолчанию.
2. **На каждом backend slice:** unit tests + focused PostgreSQL integration для
   persistence/River; миграция up/down; commit/rollback; transactional enqueue;
   тесты на подлинных временных файлах; не только mock filesystem при проверке
   ownership/exposure.
3. **После API изменений:** только `task generate` создаёт generated frontend
   client; `task verify` проверяет согласованность и полный проектный gate.
4. **Финальный локальный gate:** для будущей реализации выполнять только
   `task verify` согласно репозиторным инструкциям. Не запускать ad-hoc suites как
   замену полного gate. Платформенную matrix и Compose smoke выполнять средствами
   GitHub CI, не локальными кросс-сборками.
5. **Документация и статус:** источником утверждённых новых продуктовых решений
   остаётся `docs/design/decisions.md` или owner-decision appendix этого плана с
   явной датой/подтверждением и ссылкой. DRAFT можно перевести в implementation
   ready только после решения блокирующих пунктов. В `done/` перемещать только
   после независимого review и принятой evidence.

Никакие новые env vars, произвольные file size limits/quotas, пересэмплирование,
кодек-профили, дефолтные правила записи тегов, физические пути, digest-обязательства
или поведение автоматического quality winner данным планом не утверждаются.

## Приложение A — owner decisions от 2026-10-17

**Статус после дополнения: DRAFT.** Приложение фиксирует только решения,
явно подтверждённые владельцем 2026-10-17 в текущем диалоге. D01 согласован
частично; D02, D03, D04 и D05 остаются открытыми. План не переходит в
implementation ready, implementation не разрешена, исторические разделы выше не
переписаны. Всё, что не перечислено ниже, остаётся открытым.

### A.1 Подтверждённые решения по D01

1. **Path template.** Путь публикации задаётся конфигурируемым относительным
   path template. Конкретное значение default template не утверждено.

2. **Artist credits.** `ALBUM_ARTIST` и `TRACK_ARTIST` — полный credit.
   `ALBUM_PRIMARY_ARTIST` и `TRACK_PRIMARY_ARTIST`: если артист один, он
   определяется автоматически; если несколько — требуется явный выбор в UI.
   При отсутствии выбора использование primary token блокирует публикацию.
   Теги сохраняют полный credit, а не только primary artist.

3. **Общие переменные.** Подтверждены `ALBUM_NAME`, `TRACK_NAME`, `ALBUM_YEAR`,
   `DISC_NUMBER`, `TRACK_NUMBER`; числовые переменные поддерживают форматирование
   `:02`. `EXT` — фактический контейнер выходного файла. Подтверждены полные и
   короткие MBID, а также стабильные локальные IDs артиста, релиза и позиции.
   Primary-artist ID tokens scoped к релизу/позиции (например,
   `ALBUM_PRIMARY_ARTIST_MBID_PART`, `ALBUM_PRIMARY_ARTIST_ID` и аналогичные).
   Для релиза используется release MBID, не release-group MBID.

4. **Fallback.** Синтаксис `{A | B | "literal"}` выбирает первое непустое
   доступное значение. Неизвестный token или ошибка вычисления не являются
   основанием для fallback.

5. **Скрытые defaults отсутствуют.** Отсутствующая или пустая переменная не
   подменяется неоговорённым значением.

6. **Release type.** Подтверждены `ALBUM_PRIMARY_TYPE`, `ALBUM_SECONDARY_TYPES`
   и `ALBUM_TYPE`. Пример комбинированного представления — `Album - Remix`;
   при первичном типе `EP` или `Single` — соответственно `EP` или `Single`.
   Локальные типы без MusicBrainz
   допустимы.

7. **Various Artists.** Various Artists определяется специальным MBID, а не
   именем. Для сборника используется общий каталог; артист трека не выбирается
   автоматически. Локальный аналог Various Artists — явная отметка.

8. **Коллизии.** При коллизии добавляется стабильный position suffix; выбранное
   имя закрепляется; существующее имя не переименовывается; при новом конфликте
   suffix удлиняется. Учитывается filesystem semantics для case/Unicode.
   Коллизии каталогов разных релизов не разрешается смешивать молча.

9. **Идентичность.** Идентичность сущности/владельца в БД не выводится из MBID
   или имени.

10. **Владение файлом и замена.** Чужой файл можно явно заменить через UI.
    Свою публикацию другой позиции перезаписывать нельзя. Внешнее изменение
    своего опубликованного файла — отдельный конфликт.

11. **Изменение metadata/template.** Само по себе не перемещает и не
    переименовывает опубликованный файл. Reorg и replacement — только явные
    действия.

12. **Replacement и ошибки.** При явной замене, включая mp3 → flac, старый файл
    удаляется. Ошибка подготовки сохраняет старый файл. Незавершённый cleanup
    даёт видимый статус незавершённой замены, а не полный успех; замена
    восстанавливается после перезапуска и не создаёт две активные публикации.

13. **Пустые metadata.** Пустые metadata хранить можно, но публикация
    блокируется без release artist, release title и track title — независимо от
    выбранного шаблона.

### A.2 Что осталось открытым

- Значение default template.
- Правила отсутствующего года и иных optional components (сверх
  подтверждённого синтаксиса fallback).
- Точные правила sanitization и длины; полная case/Unicode-нормализация сверх
  подтверждённой обработки коллизий.
- Плоский layout против release directories и размещение assets явно не
  закрыты.
- D02 — выбираемый source, audio stream и сегмент.
- D03 — режим `source`/`mka`, теги и значения.
- D04 — качество, выбор и автоматический режим.
- D05 — assets, lifecycle, ошибки и изменения.

### A.3 Границы приложения

Приложение не утверждает длины IDs, конкретные правила sanitization, форматный
scope, качество, поведение assets, обязательные defaults или иные
неперечисленные продуктовые решения. До закрытия D01 и D02–D05 действуют
прежние acceptance gates раздела 8.

### A.4 Уточнения владельца по D02/D04 от 2026-10-17

Следующие ответы получены после A.1–A.3; они уточняют соответствующие открытые
пункты, но не закрывают D02/D04 целиком и не разрешают implementation:

- Первичная публикация выбирает источник автоматически по качеству, а не
  требует ручной публикации каждой позиции. Используется лучший из доступных
  проверенных источников. В automatic mode появление источника более высокого
  качества приводит к перепубликации; замена равным/худшим по-прежнему запрещена.
- Явный ручной выбор источника с публикацией закрепляет manual mode: дальнейшие
  автоматические замены запрещены. Возможность явного возврата в automatic mode
  предложена владельцем, но точное действие и его последствия ещё требуют
  согласования. Формула качества и поведение ties/unknown остаются открытыми.
- Несовпадение size/mtime не должно означать публикацию непроверенного файла.
  Владелец допускает автоматический переанализ либо обращение к оператору;
  конкретный вариант ещё не выбран. Другие доступные проверенные источники
  остаются кандидатами для автоматического выбора.
- Ноль audio streams — ошибка; один выбирается автоматически; несколько требуют
  явного выбора в UI с отображением языка, названия, кодека и каналов.
- Источник «альбом целиком + CUE» не публикуется целиком вместо отдельных
  позиций. Точные правила использования CUE, границ/точности extraction,
  отсутствующей или устаревшей разметки остаются открытыми. Предложение
  публиковать весь stream при отсутствии границ не утверждено.
- Для неподдерживаемого формата — явная ошибка, без транскодирования или выбора
  другой аудиодорожки как fallback (подтверждено владельцем «пока да»).

Повторная проверка документации: `docs/design/decisions.md` явно откладывает
формулу качества на отдельное согласование (раздел о `quality_policy_version`).
Приоритеты lossless/lossy, bitrate, sample rate, bit depth и channels этим
приложением не устанавливаются; демонстрационная MP3 → FLAC эвристика прототипа
не является утверждённой quality policy.

### A.5 Согласование качества и переноса CUE от 2026-10-17

Владелец подтвердил следующие предложения после повторной проверки отсутствия
ранее утверждённой формулы качества в документации:

- Подтверждённый lossless предпочтительнее lossy.
- Для lossless при одинаковом числе и назначении каналов сравниваются сначала
  bit depth, затем sample rate (большее значение предпочтительнее).
- Для lossy одного кодека больший bitrate предпочтительнее. Разные lossy-кодеки
  и разные channel layouts автоматически не ранжируются.
- Неизвестные характеристики не считаются доказательством улучшения.
- При равенстве текущая публикация сохраняется; для первой публикации из равных
  кандидатов применяется стабильный технический порядок. Конкретный порядок
  ещё должен быть описан техническим дизайном. Эта политика предпочтения
  характеристик не обнаруживает апсемпл и не доказывает происхождение записи.
- При несовпадении size/mtime автоматически запускается переанализ. Пока он
  выполняется, доступны другие проверенные кандидаты. Если переанализ не удался,
  оператор видит ошибку. Существующая публикация не понижается, manual mode
  не снимается.
- Действие «Вернуть автоматический выбор» показывает preview выбранного
  источника и необходимости замены. Подтверждение включает automatic mode;
  равное/худшее качество текущую публикацию не заменяет.
- **Изменение scope по явному решению владельца:** публикация источников
  «альбом + CUE» и позиций с CUE/manual segment boundaries откладывается на
  отдельный шаг/план. Если обнаружены такие разбивки, UI явно сообщает, что
  публикация сегментов пока не поддерживается. Нельзя игнорировать границы и
  публиковать целый исходник вместо позиции. Это решение заменяет предложение
  о реализации segment extraction в текущем этапе, но не удаляет существующие
  source links или разметку и не меняет matching semantics.

Требования P03/P04/P09 и матрицы к extraction сегментов в исходном тексте
относятся к отложенному scope; в текущем этапе проверяется явное сообщение
о неподдерживаемом сценарии без публикации целого образа. До реализации
необходимо отдельно оформить план отложенной поддержки. Другие открытые
решения D01–D05 этим дополнением не закрываются; статус остаётся DRAFT.

### A.6 Метаданные и предложение M4A от 2026-10-17

Владелец подтвердил следующие принципы D03:

- MKA — remux без перекодирования, несовместимый кодек даёт явную ошибку.
- Writer использует текущие локальные метаданные релиза/позиции с явными
  overrides. Подготовка и согласование provider/source/manual значений относятся
  к предыдущим этапам, а не к публикационному writer. Это не утверждает, что
  все необходимые поля и правила подготовки уже реализованы.
- Явно пустое необязательное поле удаляет соответствующий тег; отсутствующее
  значение не восстанавливается молча из исходника.
- Согласованный набор тегов записывается заново; сохранение неизвестных
  пользовательских source tags не обещается. Artwork/lyrics требуют D05.
- Preview показывает намерение; после публикации отдельно отображаются
  фактический readback и ошибки проверки.

Владелец предложил расширить варианты до исходного контейнера без
перекодирования, MKA без перекодирования и M4A с возможным перекодированием.
Запрос требует технической оценки и дальнейшего согласования: политика
ALAC/AAC, параметры AAC, допустимость lossy → lossy и изменение запрета
автоматического понижения аудиокачества пока не утверждены. M4A не является
универсальным контейнером для всех исходных кодеков; нельзя считать расширение
гарантией совместимости с любым Apple-плеером или готовности tag mapping.

### A.7 Уточнение разрешения M4A от 2026-10-17

Владелец разрешил необходимое перекодирование при выборе M4A без отдельного
переключателя-разрешения. UI при выборе контейнера явно сообщает о возможности
перекодирования. Это заменяет предложение об отдельном разрешении lossy → AAC;
конкретные параметры кодирования и окончательная codec policy ещё не выбраны.
Запрос владельца на проверку произвольных M4A-тегов, имеющегося TagLib writer
и совместимости MBID/других полей с Navidrome/OpenSubsonic является технической
задачей исследования, а не разрешением молча терять необходимые теги или
использовать FFmpeg как единственный tag writer.

### A.8 Writer, целевой reader и M4A codec policy от 2026-10-17

Владелец явно подтвердил:

- Для M4A AAC/ALAC копируются без повторного кодирования; другой lossless
  преобразуется в ALAC, другой lossy — в AAC. Отдельного разрешения
  перекодирования нет; действует предупреждение A.7. Параметры AAC и поведение
  при неподдерживаемых ALAC характеристиках ещё требуют согласования.
- **Единственный tag writer для всех поддерживаемых форматов — TagLib.**
  FFmpeg только подготавливает чистую аудиодорожку, выполняет remux и необходимые
  согласованные перекодировки; запись публикационных тегов через FFmpeg
  не допускается. Это правило относится также к MKA; отсутствие writer support
  нельзя обходить записью тегов через другой инструмент без нового решения.
- Текущий целевой потребитель опубликованных файлов — Navidrome. Mapping тегов
  для каждого поддерживаемого формата должен быть совместим с чтением Navidrome,
  включая MusicBrainz IDs и согласованный состав полей. Хранение тега в файле не
  означает гарантии его выдачи OpenSubsonic API или отображения любым клиентом.

Технический gate D09: проверить возможности именно используемой версии
TagLib/wrapper для каждого формата, особенно MKA, включая реальные write/readback
и Navidrome mapping. Если нужный формат или поле не поддерживается, вернуть
вопрос владельцу; не подменять writer и не объявлять тег записанным. Состав
полей, assets и оставшиеся продуктовые пункты по-прежнему требуют закрытия.

### A.9 Исключение для MKA от 2026-10-17

После A.8 владелец уточнил: для MKA разрешено исключение из TagLib-only writer,
предложив использовать существующую библиотеку чтения Matroska. В проекте это
`github.com/remko/go-mkvparse`. Возможность записи этой библиотекой ещё должна
быть подтверждена; само наличие reader не доказывает writer capability.
Если записи нет, выбор другой зависимости возвращается владельцу. Для прочих
форматов сохраняется TagLib-only правило; FFmpeg по-прежнему только готовит
аудио. Поддержка MKA не исключается из scope этим уточнением.

### A.10 Единый TagLib reader/writer и удаление native capture от 2026-10-17

Владелец согласовал переход на `deluan/go-taglib` для чтения и записи всех
целевых форматов, включая MKA, с удалением `go-mkvparse`. Для MKA достаточно
нормализованной плоской карты; сохранение вложенности, targets, языкового
контекста и бинарных тегов в отдельном native capture больше не требуется.
Обложки обрабатываются отдельно и этим упрощением не исключаются.

Владелец явно отверг предложение сохранять старые native snapshots как legacy:
проект находится в разработке, ненужный native capture нужно удалить целиком
из модели, persistence, API, UI и тестов, а не оставлять compatibility layer.
Это заменяет прежние требования native Matroska capture и предложение сохранять
существующие snapshots. Исторические свидетельства в завершённых планах не
переписываются. Конкретная очистка схемы является технической задачей.

Запись согласованных тегов по-прежнему должна проверяться на готовых файлах
и на совместимость с Navidrome. Использование того же wrapper в Navidrome
обосновывает выбор интеграции, но не заменяет проверки нашего writer/mapping.

Уточнение текущего состояния: technical analysis уже сохраняет все audio
streams; matching eligibility требует ровно один. Publication ещё не
реализована. Вопрос владельца о текущей поддержке нескольких потоков не является
новым решением об их поддержке/запрете и сам по себе не отменяет A.4.

### A.11 Assets, lifecycle и границы удаления от 2026-10-17

**Статус: DRAFT.** Дополнение фиксирует решения владельца по части D05 и
уточняет автоматический/manual lifecycle. Это не полное закрытие D05 и не
разрешение implementation. Разделы A.1–A.10 и остальной текст выше сохранены
как исторические свидетельства и не переписаны.

1. **Audio без cover.** Публикация audio разрешена при отсутствии artwork.
   Ошибка загрузки cover не блокирует audio: остаётся прежняя опубликованная
   обложка либо отсутствие обложки, а UI показывает warning с действием retry.

2. **Одна обложка на релиз.** Для релиза допускается одна cover. Источник по
   умолчанию — provider, затем fallback на embedded source artwork. Ручная
   загрузка/выбор обложки переопределяет автоматический выбор и закрепляет
   manual mode. Возврат к автоматическому выбору — только явное действие
   оператора.

3. **Embedded vs sidecar.** С учётом flat layout владелец **склоняется** к
   embedding обложек в аудиофайл вместо sidecar. Это зафиксировано именно как
   предпочтение, а не как окончательное решение: варианты sidecar этим пунктом
   не закрыты и требуют отдельного подтверждения.

4. **Lyrics.** Lyrics опциональны. Публикуется только одна synced-версия:
   предпочтительно уже существующий synced source, иначе LRCLIB. Unsynced
   lyrics не публикуются.

5. **Форма lyrics.** Synced lyrics встраиваются, если Navidrome это
   поддерживает; иначе используется sidecar. Проверка фактической поддержки
   Navidrome остаётся технической задачей; это условное решение владельца,
   а не произвольный выбор реализации.

6. **Ошибка lyrics.** Неудача получения/записи lyrics не блокирует публикацию
   audio: файл публикуется без lyrics, UI показывает warning с retry/выбором
   источника.

7. **Автоматические изменения.** Изменения metadata/assets в automatic mode
   публикуются автоматически через staging/replacement по действующим правилам
   замены. Ручные изменения помечают публикацию как dirty; оператор применяет
   изменения по отдельности или все pending — скрытого auto-apply ручных правок
   нет.

8. **Удаление source.** Удаление источника после публикации — opt-in настройка,
   по умолчанию **OFF**. Удаление допускается только после успешной публикации и
   validation. Включение настройки позже не запускает retroactive удаление:
   доступны точечное действие и bulk-операция в UI. Ошибка удаления — warning,
   а не провал публикации; UI даёт точечный и bulk retry.

9. **Неоднозначная формулировка владельца.** Фраза «Публикуем самую
   качественную, ... удаляем все связанные» **не** утверждает семантику удаления
   для shared sources/positions. Требуется явный ответ владельца, что именно
   считается связанным: все альтернативные входы одной опубликованной позиции
   или также source/location, нужные другим позициям/релизам, дубликаты путей и
   т.п. До ответа зависящая от этого семантика удаления не реализуется.

10. **Удаление root/location.** Удаление source root/location всегда оставляет
    publication bytes, опубликованные metadata и quality. Оно не
    является удалением публикации и не ухудшает опубликованное качество.

11. **Удаление публикации.** Добавляется явное действие удаления публикации.
    Внешнее изменение опубликованного файла требует отдельного подтверждения
    перед удалением; замена рассматривается как конфликт по A.1.

12. **Граница job.** Один job обрабатывает одну позицию (track/position) со
    всеми её sidecars и metadata. Успешно опубликованные позиции остаются
    валидным частичным результатом релиза; частичный успех не выдаётся за полный.

13. **Техническая оговорка.** Кросс-путевая «атомарность» (несколько файлов,
    assets, DB и FS) не гарантируется этим планом. Технический дизайн обязан
    описать реальные границы и recovery, а не постулировать атомарность, которой
    нет.

Legacy native capture не возвращается: действует A.10. Незакрытые части D05
(точный формат/приоритеты assets сверх перечисленного, окончательное решение
embedding обложки, объём удаления связанных источников) остаются открытыми;
статус плана — DRAFT.

### A.12 Удаление альтернативных исходников и provenance от 2026-10-17

Владелец уточнил A.11:

- При включённом удалении исходников после успешной публикации и валидации
  позиции удаляются все её альтернативные исходники, включая менее качественные.
  Цель — единая переорганизованная медиатека с приоритетом качества; это не
  ограничивается удалением только выбранного для аудио файла.
- Общий исходник не удаляется, пока он нужен другим связанным позициям/релизам:
  должны завершиться все связанные публикации и проверки. Удаление не должно
  лишать ещё не опубликованную позицию нужного источника. Правила OFF по умолчанию,
  отсутствия retroactive auto-delete и warning/retry из A.11 сохраняются.
- Источник происхождения опубликованного файла как сохраняемая функция не нужен.
  Хранится текущее состояние публикации, metadata и достаточные характеристики
  качества для обнаружения улучшений и необходимости обновления. Не создавать
  архив происхождения или сохраняемые historical source snapshots ради legacy.
  Это не отменяет временные execution inputs/claims для безопасного выполнения
  незавершённой операции и не требует удаления действующего source inventory.
- Для обложки владелец подтвердил направление «только embedding, без
  cover.jpg/png рядом», запросив возможные технические возражения. Обнаруженная
  несовместимость формата должна возвращаться на согласование, а не молча
  добавлять sidecar.

Вопрос synced lyrics из других исходников остаётся открытым: владелец указал
на возможное смещение таймингов и запросил рекомендацию о переходе к LRCLIB.
Нельзя считать lyrics другой версии аудио автоматически совместимыми или
самостоятельно вводить retiming/пороги совпадения длительности.

### A.13 Согласованный порядок synced lyrics от 2026-10-17

Владелец подтвердил предложенный порядок и уточнил пропущенное «не» в A.12:

1. Сначала synced lyrics из выбранного аудиоисточника.
2. При их отсутствии — LRCLIB с названием, исполнителем, альбомом и длительностью
   выбранной аудиодорожки.
3. Автоматического переноса lyrics из других связанных исходников нет.
4. При смене аудиоисточника lyrics разрешаются заново; прежние таймкоды не
   переносятся вслепую.
5. Автоматический сдвиг таймингов не входит в текущий этап. UI предоставляет
   просмотр, ручной выбор и возможность убрать неверный текст.

LRCLIB не гарантирует совпадение таймингов конкретной версии аудио. Данный
порядок не утверждает произвольных duration thresholds или скрытого retiming.

**Уточнение 2026-10-24:** для quality upgrade пункт 4 не применяется — аудио
обновляется без изменения метаданных, CURRENT lyrics сохраняются и заново не
разрешаются. Уточнение не переписывает исторический текст A.13 и не
переопределяет иные случаи смены аудиоисточника. Авторитетная запись — в
[decisions.md](../../design/decisions.md).

### A.14 Оставшиеся решения D01–D05 от 2026-10-17

Владелец ответил на итоговый список вопросов:

- Default path должен группировать релизы по типу: добавить отдельный компонент
  `ALBUM_TYPE` либо `ALBUM_PRIMARY_TYPE` перед компонентом
  `{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]`.
  Базовый предложенный шаблон использует primary album artist с MBID/local ID,
  затем каталог релиза и `{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`.
  Конкретный выбор между двумя type tokens, fallback отсутствующего типа и
  вопрос разрешения общего каталога разных релизов ещё требуют уточнения.
- AAC: фиксированный bitrate с ориентацией на исходник, например MP3 256 → AAC
  256 kbit/s, источник 320 → AAC 320 kbit/s. Цель — максимально сохранить
  качество исходника. Это не означает эквивалентности качества разных кодеков.
  Правила unknown/VBR bitrate, других кодеков и числа каналов ещё не определены;
  параметры ALAC вне поддерживаемого диапазона этим ответом не согласованы.
- Утверждён базовый состав тегов при наличии локального значения: release/position
  titles; полные credits и отдельные списки артистов; номера и общее количество
  треков/дисков; release/original release dates; release types, genre; artist,
  album-artist, release, release-group, recording и release-track MBIDs; ISRC,
  label, catalog number, barcode, release country/status; выбранные cover и
  synced lyrics. Обязательного заполнения всех этих полей нет. Дополнительные
  поля Navidrome нужно исследовать и предложить, а не выдумывать значения или
  обязательные пользовательские теги.
- Единственный проверенный пригодный источник может публиковаться автоматически
  при неполных quality characteristics. Несколько несравнимых кандидатов требуют
  ручного выбора. Для автоматической замены нужно доказанное улучшение.
- Manual authority независима: ручная правка конкретного тега защищает только
  этот тег от автоматического изменения; ручной выбор cover/lyrics защищает
  соответствующий asset; ручной выбор source защищает только смену source/audio
  stream. Остальная автоматика продолжает работать. Ручные изменения применяются
  явно по правилам pending из A.11.
- Автоматический старт готовых позиций разрешён после уверенного automatic
  matching, ручного подтверждения matching или подтверждения local-only релиза.
  Не требуется ждать готовности всего релиза. Улучшение опубликованной позиции
  не зависит от готовности остальных позиций.
- При отсутствии provider cover автоматический выбор embedded cover стабилен
  по порядку позиций релиза; оператор может изменить выбор вручную.
  **Уточнение 2026-10-24:** порядок позиций релиза заменён сравнением разрешения
  фактически сохранённого изображения (ширина × высота в пикселях) и минимальным
  ID кандидата при равенстве; см. текущий статус выше. Провайдер по-прежнему
  приоритетен, а fallback берётся только из сохранённых в БД данных анализа.

Эти ответы не выбирают неоговорённые fallback/encoder profiles. Неотвеченные
подпункты остаются открытыми до явного уточнения владельцем.

### A.15 Закрытие последних уточнений от 2026-10-17

Владелец подтвердил:

- Default template:
  `{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}`.
  Пользовательский шаблон может явно складывать разные релизы в общий каталог.
  Конфликтующие файлы различаются position suffix согласно A.1; технический
  дизайн должен отличать явно общий layout от непреднамеренной коллизии.
- AAC: для MP3 сохраняется измеренный bitrate аудиопотока; для VBR используется
  средний bitrate; для других lossy-кодеков и при неизвестном bitrate —
  AAC 320 kbit/s для stereo. Multichannel в AAC пока не перекодируется: UI
  показывает ограничение. Если lossless нельзя представить в ALAC без потерь,
  возвращается ошибка без скрытого уменьшения разрядности или сведения каналов.
- Дополнительные поддерживаемые metadata: sort names названий/артистов;
  composer, lyricist и другие contributors с ролями; work/work MBID и movement
  name/number/count; disc title, release version; compilation, explicit/clean;
  mood, BPM, grouping, comment; copyright, license, website, language, media.
  При наличии локальных значений они входят в mapping; отсутствующие значения
  не выдумываются и заполнение не становится обязательным.
- Для дополнительных полей требуется сохранять источники их значений
  (metadata field provenance) и предоставить локальное заполнение. Это
  происхождение metadata, а не отменённая A.12 история аудиоисточника публикации.
- ReplayGain/R128 выносятся в отдельный этап; вычисление и публикационный
  mapping этих значений не реализуются в текущем scope без нового согласования.

Ответы A.1–A.15 являются актуальным owner contract вместо незакрытых вопросов
исходного DRAFT. Перевод плана в implementation ready требует свести контракт,
оформить отложенные CUE/ReplayGain этапы и завершить технические D06–D10 с
независимым review. Это технические гейты, а не необходимость повторного
подтверждения уже перечисленных решений владельца. Доказанная несовместимость
или новый продуктовый выбор возвращаются владельцу отдельно.

### A.16 Lyrics при смене аудио и mono AAC от 2026-10-17

Владелец подтвердил следующие уточнения:

- При автоматической замене аудиоисточника вручную выбранные lyrics с
  неподтверждённой совместимостью таймингов не переносятся в новый файл.
  Аудио обновляется без спорного текста; ручной выбор сохраняется pending
  для проверки оператором, manual authority не снимается.
- Для mono других lossy-кодеков либо неизвестного bitrate выбран fallback
  AAC 160 kbit/s; для MP3 сохраняется правило измеренного bitrate.
  Это профиль кодирования, а не уменьшение числа каналов и не утверждение
  эквивалентного качества. Multichannel transcode по-прежнему не поддерживается.
- Если точный LRCLIB lookup не дал synced lyrics, аудио публикуется без текста.
  Поиск других вариантов доступен для ручного выбора в UI; автоматического
  выбора другой найденной версии нет.

**Уточнение 2026-10-24:** для quality upgrade действует текущее правило —
CURRENT lyrics и остальные метаданные сохраняются, аудио обновляется без их
изменения. Первый пункт A.16 о непереносе ручных lyrics относится к иной смене
аудиоисточника и для quality upgrade не применяется.

### A.17 Изменение занятого источника

Владелец выбрал запрет удаления каталога-источника и изменения его пути,
пока незавершённая публикация удерживает его файлы. Это относится и к очереди:
автоматически отменять queued-публикации ради удаления не требуется.
Неразрешённое исполнение после сбоя сохраняет защиту до безопасного завершения;
одного terminal operation state недостаточно для снятия удержания.
Уже опубликованные файлы, их metadata и quality при последующем удалении
источника сохраняются согласно ранее согласованным правилам.

**Уточнение от 2026-10-23:** требование retained protection после сбоя не
создаёт publication recovery/quarantine protocol и не отменяет terminal visible
error/manual retry из текущего контракта выше. Оно не разрешает source cleanup до
подтверждённого DB-результата; актуальное opt-in и consumer protection проверяются
перед cleanup. Историческая формулировка A.17 не расширяет execution guarantee.

<a id="publication-technical-design"></a>

## Технический дизайн файловой публикации

> **Технический snapshot подготовлен 2026-10-18; не текущий статус реализации.**
> Исторические сведения о коде и dated review evidence ниже не переписываются.
> По прямому решению владельца 2026-10-23 требования execution в разделах 6, 7,
> 10 и 11 обновлены: эти разделы теперь описывают текущий технический контракт,
> а не протоколы recovery прежнего snapshot. Остальные owner decisions A.1–A.17
> сохраняются; текущий статус реализации указан в начале плана.

### Историческая отметка статуса технического дизайна

**Статус в snapshot от 2026-10-18:** после независимого review разрешены
подготовительные срезы identity/read model, metadata/primary artist и unified
reader/native removal. Exposure/tool mutation оставалась за отдельными
техническими гейтами D09 и execution exclusion. Review от 2026-10-17 не являлось
runtime evidence. Технический дизайн развивал D06–D10 плана с учётом решений
A1–A16, не менял owner contract и не закрывал оставшиеся технические гейты.
При противоречии продуктовых формулировок приоритет был у A1–A16 и `docs/design/`;
обозначенное «предложением» подлежало техническому review, неизвестные требования
не заполнялись предположениями.

### 1. Границы, предпосылки и проверенная текущая реализация

Дополнение 2026-10-18 (startup barrier): перед platform
initialization и output-reset recovery в `app.Run` добавлен read-only
repeatable-read startup barrier. Retained execution claims и несогласованные
attempts запрещают дальнейший запуск; queued/unclaimed admission проверяется
по actual River delivery и namespace reservations. Пустой snapshot не доказывает
interprocess exclusion; barrier не разрешает takeover, recovery mutation,
settlement или регистрацию publication worker. Полный локальный `task verify`
среза прошёл; native CI для него ещё не проверен.

Дополнение 2026-10-18 (bounded lyrics slice): generic position metadata допускает
ручное сохранение валидного synced LRC текста как desired value с manual authority
и optimistic metadata revision. Это только pending-save/read-model foundation:
apply, выбор LRCLIB/source, review UI и публикация lyrics этим не реализованы.
Desired текст не добавляется в обычную tag projection и не изменяет текущий
published asset.

Цель — одна актуальная управляемая публикация на позицию релиза: извлечённое аудио,
локальные теги, встроенная обложка и допустимые synced lyrics, с подтверждённым
readback, восстановлением после сбоя и без ложного статуса успеха. Одна River job
обрабатывает одну позицию со всеми её согласованными assets. Частичный успех релиза
остаётся частичным.

План 12, включая **все A1–A16**, является источником продуктовых решений.
В частности, current plan требует автоматического выбора при уверенном качестве,
manual authority по каждому полю/source/asset отдельно, независимую подготовку audio/
cover/lyrics, явные pending-изменения, removal OFF по умолчанию и отсутствие
retroactive удаления. CUE/manual segments и ReplayGain/R128 отложены. D09 ниже —
техническая проверка, не разрешение заявлять поддержку форматов либо лицензий.

На момент подготовки плана в `backend/internal/publication/` нет рабочей публикации;
таблиц/API/jobs publication нет. Это историческая исходная точка, не текущее
утверждение: подготовительный foundation теперь содержит publication tables и
River job kinds, но worker ещё не зарегистрирован; утверждать отсутствие всех
publication jobs уже неверно. Есть локальные сущности и matching,
`publication_format` (`source`/`mka`), source analysis, а output reset уже имеет
durable coordinator. Эти факты требуют перепроверки перед реализацией.
В `backend/internal/jobs/move_publication.go` и
`backend/internal/jobs/install_publication.go` уже применены журналы владения и
проверки файлов; они являются локальными примерами, не готовым протоколом
публикации.

Архитектурные примеры, подтверждённые кодом:

- `backend/internal/service/source_technical_analysis.go` сохраняет распознанные
  технические данные и tags анализа; это не metadata writer/readback контракт.
- `backend/internal/jobs/move_publication.go` фиксирует operation identity,
  target и ownership witness в journal и не считает совпадение пути доказательством
  владения.
- `backend/internal/jobs/install_publication.go` пишет durable publication
  journal, проверяет conflicts и делает rollback по ownership evidence.
- Перед кодом сверить текущие `release_track`, `track_source`, operation claims,
  reset и lock flows; этот документ не утверждает, что существующие коды API,
  имена колонок или lock order уже совпадают с предложениями ниже.

### 2. Архитектурные границы и сохраняемая идентичность

Слои: API (Huma) → service → persistence; только persistence обращается к Bun/DB.
`backend/internal/publication/` предлагается как владелец filesystem planner,
private staging, extraction/writer orchestration и readback. Jobs владеют River
delivery и вызывают service. UI — только через API; клиент генерируется
Orval. Не добавлять env vars, auth, файловые записи в API/persistence или ручные
правки generated client.

#### Предложение: стабильный ID позиции

Текущая логическая идентичность `release_track` — составной ключ
`release_id`/medium/position, без UUID; текущий `selected track_source` связан с
`local_track`, а не отдельной release position. Публикация же адресуется
позиции. Добавить UUID `release_track.id` как additive migration и использовать
его как стабильный технический identity в paths/suffix, publication refs,
jobs, locks и claims. Backfill существующих рядов один раз; новые позиции
получают новый UUID, перемещение/переименование не меняет его. Сохраняются
уникальность/семантика существующего составного ключа и все FK; план миграции
должен проверить текущие зависимости, индексы и nullable поведение.

Не использовать MBID, имена, track ID либо filesystem path как owner identity.
UUID — техническая идентичность; шаблонные переменные `ALBUM_ID`/ID позиции
разрешаются из стабильных локальных identity, а не из пути/имени. Это не новый
продуктовый выбор. `track_publication` ссылается на position UUID и гарантирует
не более одной активной записи на позицию.

#### Предложенные таблицы и ответственность

Это минимальный логический контракт, не готовый SQL; точные типы/имена
колонок — на migration slice.

| Сущность | Назначение и ограничения |
| --- | --- |
| `release_publication` | Одна запись на local release: текущий шаблон/формат и активная конфигурация, cover resolution/authority, dirty/pending summary. Не дублировать весь release metadata. |
| `track_publication` | Одна запись на position UUID: source/stream selection и authority; текущий audio target/artifact identity; фактический readback, quality characteristics/policy version, состояние; отдельная lyrics selection/authority/status; metadata snapshot только в объёме, нужном для актуальной публикации и pending. **Не** хранит завершённую source provenance/history. |
| `track_publication_attempt` | Только незавершённая попытка: operation ID, attempt/fence identity, stage, current execution inputs, warnings/errors. После settle потребляется/удаляется; не становится историческим журналом и не хранит journal/recovery authority. |
| `publication_namespace_claim` | Уникальное занятие вычисленного target по реальной FS equivalence semantics, владельцу position UUID и epoch. Отдельно представляет конфликт с чужим файлом/каталогом и подтверждённую замену. |
| `publication_asset_cache` (предложение) | Управляемый кеш provider/local cover или synced lyrics для повторного использования/retry; отделён от актуальных embedded bytes и очищается только ownership-verified путём. Не вводить без доказанной необходимости конкретной схемы. |
| `source_cleanup_item` | Незавершённая opt-in cleanup цель после валидации: candidate identity/size/mtime snapshot, список требуемых позиций/публикаций, состояние и retry warning. Нет FK на источник, который уже удалён; lifetime допускает cleanup retry после удаления link/root. |

Фактический audio provenance после settle не сохраняется (A12). Временные
execution inputs/claim допустимы для незавершённой работы; source inventory и
metadata-field provenance продолжают жить отдельно. Ошибки FK/cascade не должны
удалять файлы. Для каждого output ref требуется отдельная filesystem cleanup
операция с подтверждением ownership.

#### Primary artist selection

Сохранить выбранного primary artist как persisted reference, отдельно scoped к
release и position, со ссылкой на локальную artist identity и связанную MBID
identity, если она есть. Candidate membership строится по актуальному ordered
credit list; не хранить только строку/индекс credit и не выбирать первого
артиста автоматически, когда их несколько. При одном артисте выбор разрешается
автоматически; при нескольких требуется явный выбор. Локальный ID и MBID должны
разрешаться к одной и той же выбранной artist identity, а не независимо брать
разные позиции. Если выбранный артист удалён из membership/credit list, считать
выбор invalid: шаблон с `ALBUM_PRIMARY_ARTIST*`/`TRACK_PRIMARY_ARTIST*` блокирует
публикацию и запрашивает выбор оператора. Не выбирать нового артиста молча.

### 3. Metadata, authority, preview и критерий readback

Для каждого поддерживаемого metadata field нужна typed value с тремя
состояниями: `absent` (не менять/не восстанавливать молча из source), `value`,
`explicit_empty` (для согласованных optional fields удалить target tag).
Preview строится из текущих локальных release/position значений и явных
overrides; provider/source metadata не подмешиваются writer-ом. Обязательные
release artist/title и track title проверяются согласно A1. Согласованный набор
тегов пишется заново; произвольные source tags не обещаются.

Предлагается typed registry полей и mapping contract: normalized application
field → целевые ключи → writer/readback capability → Navidrome interpretation.
Сохранить field-level provenance для расширенных A15 полей, но не спутывать его
с provenance аудиоисточника. Preview показывает намерение; active publication
показывает отдельно фактический readback, неподтверждённые поля и warnings.
Успешный вызов `SaveTags` сам по себе не является доказательством записи.
Поля без реального readback подтверждения нельзя показывать как успешно
опубликованные; capability gap блокирует заявленную поддержку и возвращается на
review, а не маскируется.

Manual authority независима для конкретного тега, выбранного audio source/stream,
cover и lyrics. Автоматическое обновление одного поля не меняет другие
authority. Ручные изменения формируют отдельные pending значения и не
переписывают auto-current; применение — явное (по полю или все pending).
При автоматической смене аудио manual lyrics с неподтверждённой совместимостью
не встраиваются: audio публикуется без них, ручной выбор сохраняется pending и
authority остаётся manual (A16).

Quality policy — чистый versioned comparator над проверенными characteristics
аудиоисточников, а не gate для metadata/asset updates:
lossless предпочтительнее lossy; lossless при одинаковых числе/назначении
каналов сравнивать bit depth, затем sample rate; lossy того же codec — bitrate;
разные lossy codecs/layouts несравнимы. Unknown не доказывает улучшение.
Current equal сохраняется; при первой публикации equal candidates разрешаются
стабильным техническим порядком (UUID позиции/источника только как tie-break,
не как качество). Один проверенный кандидат можно публиковать с неполными
характеристиками; несколько несравнимых требуют ручного выбора. Автоматическая
замена требует доказанного улучшения. Comparator должен иметь результаты
better/equal/worse/incomparable/unknown и тест-векторы; никакого score/default
порога не добавлять. Если audio source/stream не меняется, metadata/cover/lyrics
update не сравнивается по quality и не блокируется равенством audio quality.

### 4. Source, audio, assets и инструменты

Публикационный source выбирается на position, хотя существующий link может
принадлежать общему `local_track`/variant. Claim фиксирует source, location,
audio stream, наблюдаемые size/mtime, technical-analysis identity и settings
snapshot для незавершённой попытки; это execution input, не historical source
provenance. При изменении size/mtime запускается переанализ; пока он идёт,
допускаются другие проверенные candidates, прежняя publication не понижается.
Успех переанализа должен повторно валидировать выбранный input.

Ноль audio streams — ошибка; ровно один выбирается автоматически; несколько
показываются для явного выбора с языком, title, codec и channels. Unsupported
format/layout — явная ошибка, без fallback на другой stream/транскодирование.
CUE/manual segment boundary и album+CUE не публикуются в текущем scope: сообщить
явно, не выдавать весь исходный stream за позицию. Никакого source modification.

FFmpeg подготавливает чистую аудиодорожку, remux или разрешённое A8/A15
transcode. Не passthrough metadata attachments/chapters/неаудио streams. MKA —
без re-encode, несовместимый codec — ошибка. M4A: AAC/ALAC копировать без
re-encode; иной lossless → ALAC, иной lossy → AAC. AAC: MP3 использует measured
stream bitrate (VBR — average); другие lossy/unknown bitrate: stereo 320,
mono 160 kbit/s. Multichannel AAC transcode не поддержан. Если ALAC требует
lossy reduction/уменьшения разрядности/сведения каналов — ошибка. Это не
утверждает больше форматов, чем проверено tool capability.

Единый `deluan/go-taglib` reader/writer предлагается pin-ить на commit
`32d7614f30ecf47d451cd801e0cfb93bf9739db4`; WASM 2.3.2. Это техническое
предложение, не owner-approved pin; подтвердить совместимость в narrow tool
slice. Удалить
`go-mkvparse` и native capture trees целиком — без legacy blobs, API, UI или
compatibility layer (A10). Удаление `go-mkvparse` и native capture model — явно
согласованное полное удаление A10, не legacy compatibility task. Оно не
разрешает destructive rollback живых publication refs/artifacts: для них
действуют правила безопасного down migration в разделе 11. Сначала проверить действительный supported-format
scope, реальные read/write/readback, Navidrome field mapping и build/test matrix.
Не обещать распространение/лицензионную чистоту: D09 external research пока не
runtime-verified, лицензии не являются legal approval. Обновить notices только
после проверки действительных транзитивных лицензий и packaging.

#### Artwork и lyrics

Одна cover на релиз: provider → embedded source cover; stable order позиций
релиза заменён уточнением 2026-10-24 (максимальное разрешение сохранённого
изображения, при равенстве — минимальный ID). Manual selection блокирует
автоматическую смену до явного возврата.
Только embedding, без sidecar. Cover failure не блокирует audio: прежняя cover
или отсутствие cover остаётся, UI показывает warning и retry. Несовместимость
writer/container embedding — вернуть на согласование, не вводить sidecar.

Применение новой pending cover обновляет все уже опубликованные позиции этого
релиза, а не выбранное подмножество; сохранение pending само по себе обложку не
применяет. Промоция pending и постановка jobs на затронутые позиции выполняются
одной admission-транзакцией по существующей схеме (`River.InsertTx`, отдельная
job на позицию). Это техническая реализация уже утверждённого контракта, а не
новое продуктовое требование; поведение занятой позиции (пропуск/ошибка) этим не
выбирается.

**Уточнение 2026-10-24:** начальный выбор cover сравнивает разрешение фактически
сохранённого изображения (ширина × высота), при равенстве — минимальный ID
кандидата; stable order позиций релиза не применяется. Оригинальные bytes
сохраняются без изменений при каждой стороне ≤10000 пикселей; большее изображение
не блокирует audio (допустима non-blocking ошибка, выбранная cover сохраняется).
В файл встраивается derivative ≤2048 × 2048, aspect fit, без upscale. Обработка
файловая через libvips, без полного массива пикселей в Go. По отдельному ответу
владельца допускаются внутренние native-буферы декодеров; это не гарантия жёсткого
предела RAM. Lossy JPEG и default byte cap не авторизованы. Реализация этих
частей присутствует; полная платформенная проверка ещё не завершена. Авторитетная
запись — в [decisions.md](../../design/decisions.md).

Только одна synced lyrics версия: сначала synced lyrics выбранного audio source,
иначе точный LRCLIB lookup по title/artist/album/duration; нет результата —
публиковать без lyrics. Никакого авто-поиска альтернативных LRCLIB matches,
переноса lyrics с других источников или auto-retiming. Unsynced не публиковать.
Ручные поиск/просмотр/выбор/удаление доступны UI; ручная authority — отдельна.
Проверить фактическую поддержку synced embedded lyrics в Navidrome; sidecar
допустим только по условному решению A11 и не становится fallback без проверки.
Provider/LRCLIB failures — warning/retry, не audio failure.

### 5. Планирование путей и namespace ownership

Использовать подтверждённый A15 default template буквально:

```text
{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}
```

Template configurable relative path. `EXT` означает фактический output
container; metadata/template edit сама не перемещает существующий output.
Неопределённые template/security details должны сверяться с последним plan12
решением; никакого path traversal/absolute path. Явно общий каталог допускается
только явно выбранным template; непреднамеренное столкновение каталогов разных
релизов — конфликт. При collision добавляется стабильный position suffix,
который закрепляется, существующее имя не меняется, при новом конфликте suffix
удлиняется. Filesystem case/Unicode equivalence определяется существующим probe,
а не PostgreSQL collation. Чужой файл можно заменить только после явного UI
подтверждения; файл другой публикации не перезаписывается. Внешне изменённый
owned file — конфликт. Sanitize/length limits и unresolved cross-release shared
directory behavior — implementation gate, не здесь выдуманный default.

Namespace claim резервирует вычисленные destinations и parent namespace в
соответствии с пробой FS semantics; уникальность проверять под transaction/claim
и на реальной FS. Файловая проверка на этапе exposure повторяется. Нет удаления
неизвестных файлов и «исправления» коллизии через overwrite.

### 6. Durable operation, fencing и atomic replace

#### Admission, coordinator и execution

Admission и `River.InsertTx` выполняются одной Bun-транзакцией; River args
содержат только operation ID. Worker заново загружает полный актуальный input.
Общий coordinator сериализует writes одного destination/позиции, ручные
изменения, reset и delete; stale manual selection и чужой target owner не
допускаются. Это не требует SQL-транзакции во время подготовки или ffmpeg.
Необходимые claims/fences защищают фактическую работу; после подтверждённого DB
result достаточно дождаться завершения собственных live callbacks и освободить
обычные locks результата. Не вводить бесконечный recovery fence, второй recovery
coordinator, durable handoff или startup recovery protocol.

#### Whole-result lifecycle

```text
queued → preparing (fresh full candidate) → validated/readback → atomic replace
        → one DB result transaction → operation succeeded
failure before DB commit → visible failure/retry; next delivery prepares whole result
```

Кандидат готовится приватно рядом с фактическим destination на той же FS. После
валидации native atomic replace публикует целый файл. Одна DB-транзакция фиксирует
actual projection и успешную operation, освобождает holds и потребляет attempt.
Никакой SQL transaction не удерживается во время ffmpeg. Сбой до DB commit
повторяет полную подготовку, включая случай, когда предыдущая delivery уже
заменила target; откат, точное продолжение шага и exactly-once не требуются.
Неизвестность до подтверждения DB commit не считается успехом.

Rewrite без source строится чтением текущего destination. Metadata/lyrics
changes должны сохранять audio bytes и artwork, не выполнять lossy re-encode;
проверять actual output readback. При смене пути или расширения артефакта в
пределах текущего output новый DB result и durable cleanup intent создаются в
одной транзакции, а удаление прежнего managed файла выполняется после commit
отдельно со свежими проверками; cleanup failure — обычный warning, не
source-cleanup recovery state. Смена или сброс output root сюда не входят, старые
физические файлы не удаляются.

Atomic replace допустим только поддерживаемой native-операцией на той же FS.
Delete-then-copy запрещён. Универсальная power-loss durability не обещается;
directory sync не является условием atomic replace. Нет требования byte-identical
whole-file результата между повторами. Никакой V1/V2/V3 compatibility path не
требуется. Завершённая River operation при redelivery не выполняет файловую работу.

Отмена и исчерпание retry завершают operation видимой ошибкой и позволяют ручной
retry; это не вечный running и не разрешение раннего cleanup. Достаточно дождаться
основного процесса и собственных callbacks, process-tree proof не требуется.

### 7. Output reset и удаление источников

Publication claims/in-flight attempts и source cleanup включаются в общий
coordinator и reset fencing. Claimed/running публикация блокирует reset; queued
work и его DB refs отменяются одной транзакцией с обновлением output epoch.
Recovery refs/protocol не вводятся. При reset старые publication files остаются
unmanaged, active DB refs очищаются, local entities/source links и
inventory/analysis сохраняются, автоматической republish нет. Source cleanup
начинается только после подтверждённого publication DB-result, перечитывает
актуальный opt-in и защищает queued/running consumers; opt-in OFF не запускает
cleanup. Независимые правила проверки size/mtime и shared borrowers остаются
согласно решениям владельца.

Уточнение владельца от 2026-10-21 для source removal: на любой поддерживаемой ОС
достаточна последовательная проверка размера и `mtime` по БД непосредственно
перед delete. Неатомарность и остаточная гонка внешней подмены принимаются;
атомарный compare-and-unlink, quarantine и hash не являются обязательными
условиями. Требование не удалять изменившийся/чужой путь ниже означает отказ
при обнаруженном несовпадении в этой проверке, а не гарантию против подмены после
неё. Остальные borrower/permission/hold gates сохраняются; см. `decisions.md`.

Source removal opt-in OFF. Cleanup перечитывает актуальную настройку и до unlink
проверяет актуальные publication/validation, shared borrowers и отсутствие
queued/running consumer; детали проверки size/mtime следуют отдельным решениям
владельца. Переключение opt-in не инициирует retroactive cleanup. Ошибка cleanup —
warning с ручным retry, не изменение успешного DB результата. Завершённая River
delivery при redelivery не выполняет файловую работу даже при отсутствующем
source. Удаление root/location не удаляет publication bytes/metadata/quality;
удаление публикации — только явное действие.

### 8. Изменения данных и trigger coalescing

Automatic-ready trigger после уверенного matching, явного matching confirm,
local-only release confirm и изменения локального metadata/provider value/asset
должен coalesce-ить dirty fields/positions, не публиковать все сущности при
startup. Ручная authority оставляет изменившееся значение pending. Для ready
automatic position запускается planner/comparator; improvement replacement не
ждёт остальных позиций. Защита от повторных/одновременных triggers — idempotent
unique active intent per position/config version; старый intent переоценивает
актуальные входы до исполнения либо завершается stale, не пишет устаревший.

Source freshness mismatch запускает согласованный re-analysis pathway, не
публикацию непроверенного пути. Анализ source, изменения source links, metadata
field, cover, lyrics и cleanup должны инвалидировать только зависимую область.
Output reset ставит durable suppression/epoch boundary; startup scan не должен
превращать reset в массовую auto-republish.

### 9. API/UI scope и наблюдаемость

Минимальная release detail/read model возвращает position UUID, локальные
значения/field provenance, pending authority, source candidates и quality
comparison, текущую active publication с output path, фактическим tag readback,
audio quality/format и отдельными asset/cleanup warnings. Preview показывает
intended template/path, collision, source/stream, planner inputs, mapping
outcome и validation. Mutations: source/stream choice, template/config, preview,
explicit publish/replace/retry, return-to-auto с preview/confirm, выбор cover/
lyrics, pending apply/discard, delete publication, source-cleanup retry. Все
экшены используют operation snapshot; SSE — только wake-up, после него UI
перечитывает состояние.

UI не выдаёт intention за readback, partial release за полный, warning за success,
hidden automatic decision за user choice либо формат capability за гарантию
любого файла. Huma → OpenAPI → Orval pipeline; generated code не редактируется.

### 10. Технические срезы и зависимости

Срезы уточняют P01–P10 плана, не сужая scope и не отменяя acceptance gates.

| Slice | Результат / блокеры |
| --- | --- |
| T01 — позиционная идентичность и read model | UUID release position additive migration/backfill, FK audit, release/position detail; проверка shared track/variant и uniqueness. |
| T02 — typed metadata, manual fields, provenance | Typed absent/value/explicit-empty, field registry/mapping contract, pending/authority; UI/API shape после capability map. |
| T03 — reader/writer и native removal | Pin unified deluan reader/writer; убрать go-mkvparse/native trees end-to-end; проверить format scope, actual readback и Navidrome. Не включать поле/формат без подтверждения. |
| T04 — publication schema, operation, reset | Additive tables/constraints, operation claims/epoch, River InsertTx, reset integration, safe down behavior. |
| T05 — чистый planner/template/quality | Versioned pure planner, template parser, FS namespace probe, stable suffix/claims, quality comparator vectors. |
| T06 — worker/extraction/tools | Durable job ID-only args, fresh full inputs, source freshness, FFmpeg clean audio only, writer invocation, real readback. CUE scope rejected with clear validation. |
| T07 — atomic replace/result | Private same-FS candidate, readback, native atomic replace, one DB result transaction, serialization and repeat-whole-work crash boundaries; no recovery protocol. |
| T08 — cover | Provider → stable embedded fallback, manual lock/pending, optional warning/retry, embedding capability gate; применение pending обновляет все опубликованные позиции релиза. |
| T09 — lyrics | Source synced → exact LRCLIB lookup; manual alternatives, no wrong audio transfer/retiming, Navidrome behavior gate. |
| T10 — automatic triggers + cleanup | Per-field dirty coalescing, no startup sweep; borrower-aware source cleanup with fencing, UI bulk/targeted retry. |
| T11 — API/UI, end-to-end, independent review | Generated API, truthful state, real DB/River/files/tool scenario, reset/serialization/partial cases; full task verify and independent acceptance. |

### 11. Верификация, миграции и последовательность реализации

Технический дизайн задаёт достаточно конкретный контракт, чтобы после его
независимого review начинать независимые узкие срезы: UUID позиции и модель
чтения; типизированные метаданные и выбор основного артиста; исследование
библиотеки чтения/записи тегов. Срезы, меняющие выходные файлы, обязаны сначала
реализовать и проверить взаимоисключение исполнений, atomic replace и fencing
сброса output; UI не обходит эти механизмы. Неизвестные возможности инструмента —
граница поддерживаемых возможностей: сначала исследовать и проверить, затем
включать только доказанные форматы/поля. Это не требует откладывать независимые
срезы.

Проверка реальных `deluan/go-taglib`, FFmpeg, соответствия Navidrome для тегов и
lyrics, а также упаковки должна зафиксировать версии, поддержанные форматы/поля,
readback и ограничения. Внешнее исследование D09 пока не проверено на runtime;
лицензионная проверка не является юридическим одобрением. WASM 2.3.2 и
предложенный commit — входные данные для проверки, а не доказательство
возможностей. Нельзя заявлять поддержку на основании констант/списков форматов;
при несовместимости функция остаётся явно неподдерживаемой, вопрос возвращается
владельцу, writer не подменяется.

Единственная локальная проверка проекта — `task verify` согласно инструкциям;
отдельные выборочные наборы тестов и сборки не заменяют её. Приёмка должна
включать реальные сценарии PostgreSQL-миграций, River, файловой системы и
инструментов с проверкой готового файла через readback. Матрица сбоев включает
crash после replace до DB commit и после DB commit до завершения River job; отмену
при основном процессе и callbacks; гонки с reset; устаревшие attempt/epoch/
generation; конкуренцию за target и borrowers source; ошибку БД/rollback; внешнюю
мутацию; ошибки provider/writer/tool. Повтор до DB commit готовит целый результат
заново, а завершённая job при redelivery не делает file work. Для
Navidrome проверять реальное чтение согласованных тегов/assets, не выводить
поведение OpenSubsonic API из факта записи тега.

Откат additive migration не удаляет ссылки/артефакты действующих публикаций,
активные claims или незавершённые попытки: откат отклоняется, пока зависимые
публикации не удалены штатным безопасным действием. Это ограничение не относится
к отменённым A10 native capture/schema: их полное удаление одобрено и выполняется
без сохранения legacy-совместимости. Backfill UUID/схемы не запускает публикацию
старых локальных сущностей. Проверку существующих PK/FK и транзакции reset
выполнить в T01/T04; это задача с конкретным сроком/срезом, а не бессрочный
архитектурный блокер.

#### Related deferred work

- CUE/manual segment support: explicitly deferred by plan12 A5; do not publish
  whole source as a track. See [deferred segmented publication](../to-decompose/segmented-file-publication.md).
- ReplayGain/R128: separate stage per A15; see [deferred ReplayGain/R128](../to-decompose/replaygain-r128.md).
- Source inventory/provider/matching remain plan 11 domain; publication consumes
  their verified inputs and does not redesign them.

**Историческое условие перехода:** после независимого review дизайна можно было
начинать узкие срезы в указанной последовательности. Подтверждение D09 на runtime
требовалось перед включением/заявлением конкретных возможностей. Этот snapshot не
менял решений владельца и не переписывал статус основного плана.
