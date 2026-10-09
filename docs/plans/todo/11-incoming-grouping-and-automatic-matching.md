# План 11 — группировка входящих и автоматическое сопоставление

**Дата подготовки:** 2026-10-09. **Статус:** согласованный план реализации;
реализация этим документом не заявляется. Следующий основной этап — matching,
не operations. Файловая публикация планируется следующим отдельным этапом и в
этот план не входит.

## Перед началом

- Владелец явно выбрал автоматическое сопоставление следующим основным этапом
  после плана 10; отдельное развитие экрана Operations не является следующим
  этапом. После matching ожидается план файловой публикации.
- Все продуктовые решения и окончательное уточнение о «полном релизе» ниже —
  обязательный контракт. При конфликте с макетом, upstream, текущим кодом или
  более ранней формулировкой применяется последнее решение владельца.
- Это план группировки, поиска/кэширования evidence, сопоставления и ручного
  review. **Никакой публикации**, файловой подготовки publication, изменения
  source bytes, удаления source, автоматического выбора качества, расписаний,
  периодического scan или отдельной реализации Operations UI здесь нет.
- Внешние provider endpoints, лимиты/частота запросов, cache freshness и
  durable-job lifecycle требуют технического исследования официальной
  документации непосредственно перед соответствующими реализациями. Не
  переносить upstream-поведение как факт официальной политики провайдера.
- До реализации каждому этапу нужен независимый review дизайна/схемы и точных
  API boundary. Новое продуктовое решение не принимается молча в коде или SQL;
  если ниже указанный контракт не позволяет однозначный исход, остановить
  зависимый этап и запросить решение владельца.

## Реестр одобренных решений и источников

| Область | Утверждённый контракт | Основание |
| --- | --- | --- |
| Приоритет | После staged source analysis реализуется auto matching; Operations не является промежуточным этапом; файловая публикация — следующий основной этап после этого. | Явный выбор владельца для плана 11. |
| Группировка | Сначала использовать согласованный release MBID. При его отсутствии — ALBUM + полный ALBUMARTIST, а если его нет — ARTIST; учитывать DATE и CATALOGNUMBER, если есть; папка — fallback. Группировка может объединять roots. | Решения владельца для этого плана. |
| Конфликты/отсутствия | Конфликтующие значения разделяют группы. Отсутствующее значение нельзя молча считать совпавшим или присоединять к заполненному значению. DISCNUMBER не делит альбом на группы. | Решения владельца для этого плана. |
| Издание и страна | Страна — дополнительный разделяющий признак, когда она есть в явных тегах либо выводится из разрешённого release MBID; разные страны — разные группы. Если edition информации недостаточно, одинаковые теги в разных папках не сливаются автоматически. | Решения владельца для этого плана. |
| Ручное управление | Оператор может вручную merge/split/move групп. Draft assignments живут только на странице/в сессии; подтверждённые связи сохраняются. | Решения владельца для этого плана. |
| ID и защита ручного выбора | Встроенный MBID участвует в общей matching-формуле; не превращать его в синтетические confidence=1. Подтверждённые manual links защищены от автоматического перевыбора до явного изменения оператором/возврата в automatic mode. | Решения владельца; `docs/design/decisions.md`, «Matching и целостность provider-связей». |
| AcoustID | Интеграция optional, включена при наличии настройки/доступной конфигурации; fingerprint evidence участвует в общей формуле. Её отсутствие не является совпадением и само по себе не блокирует matching. | Решение владельца для этого плана; источник данных — план 10. |
| Скоринг | Перенести одобренные legacy factors/weights, duration и fuzzy/missing semantics; auto threshold 0.70. Score — нормализованный confidence, не вероятность. | Решения владельца; upstream ниже зафиксирован по read-only clone. |
| Выбор издания для группы | Рассматривать лишь releases, прошедшие порог для **каждого файла группы**; выбрать максимальную сумму score по всем файлам только при единственном максимуме. Любая ничья требует ручного решения. Дополнительного winner-gap нет. | Решение владельца для этого плана. |
| «Полный релиз» — финальное уточнение | Для auto-apply matching достаточно, чтобы **все файлы входящей группы** уверенно получили назначения. Не требуется покрыть все позиции provider release: неполный provider tracklist coverage допустим. Если вся группа назначена, авто-решение применяется ко всей группе. Частичное auto-apply группы запрещено. | Самое позднее обязательное уточнение владельца; оно supersedes предложение «все позиции релиза покрыты» из ранней версии формулировки/макета. |
| Граница публикации | Matching может сохранять локальные сущности и подтверждённые matching/source links; он не публикует файлы и не объявляет публикацию выполненной. | Решения владельца; `docs/design/decisions.md`, `docs/design/data-model.md`. |

> **Датированное дополнение 2026-10-09:** решения владельца по ранее открытым
> gates ниже зафиксированы в
> [Приложении A](#appendix-a-owner-decisions-2026-10-09); противоречащие открытые
> gates помечены ссылкой на него. Позднейшие уточнения того же дня — в
> [Приложении B](#appendix-b-owner-decisions-late-2026-10-09); в частности,
> строка «ID и защита ручного выбора» уточнена там (B6): embedded MBID — только
> lookup/evidence, без числового веса и без forced 1.

### Upstream reference и переносимая семантика

Upstream прочитан библиотекарем в read-only clone
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/upstream_repo`,
commit `17448df1ee7ab5c9ad4769b4e09a474b27547728`. Источники для сверки —
`src/music_ingest/services/matching/scoring.py` и
`tests/matching/test_matching.py`, `test_matching_providers.py`,
`test_noize_matching_regression.py`. В плане переносится одобренная семантика,
не Python/SQLAlchemy-реализация и не upstream persistence/API contract.

- Factors: artist 4, release title 4, recording title 4, duration 2,
  MusicBrainz search 2, AcoustID fingerprint 4, track number 2, disc number 1,
  track total 1, disc total 1. Release-score использует релизные artist/title,
  duration и доступные position/provider-search factors; recording-score —
  recording title/artist, duration и доступные MB/AcoustID factors. Обе формы
  используют только применимые факторы: weighted average по available factors,
  итог снизу ограничен нулём. Missing/not-applicable не подменяется нулевым
  evidence и не добавляется фиктивным весом.
- Text similarity: upstream `default_process` tokenization/normalization,
  `token_set_ratio`, умноженный на coverage penalty
  `0.6 + 0.4 * ((token coverage + character coverage) / 2)`; считать также
  transliterated варианты через unidecode и брать максимум. Exact expected
  normalization нужно закрепить regression fixtures; не подменять её новой
  эвристикой или frontend-логикой.
- Duration: для положительных `expected`, `actual`:
  `difference=abs(expected-actual)`,
  `local_similarity=max(0, 1-difference/20)^2.2`,
  `duration_ratio=max(expected,actual)/min(expected,actual)`,
  score=`local_similarity - log2(duration_ratio)`. Некорректное/неизвестное
  значение не участвует; агрегированный confidence не ниже 0.
- Важное исключение: upstream scoring.py возвращает forced `1.0` для explicit
  MBID. **Не переносить это исключение.** Встроенные MBID — evidence общих
  release/recording scoring factors. Ручное подтверждение хранится как
  `decision_method=manual`, не как фальшивый confidence.
- Upstream `select_folder_release` детерминированно разрешает tie по MBID; для
  плана 11 это поведение заменено решением владельца: tie = manual review.
  Upstream selector пересекает кандидатов по группам и выбирает max sum; здесь
  дополнительно строгое условие qualified score для каждого файла задаётся
  порогом 0.70, а winner должен быть уникальным. Gap сверх порога не вводится.
- Перенести/regression-test существующие upstream case fixtures и edge cases,
  включая частичные доступные factors, transliteration/token coverage,
  featured/мультиартистов, mismatch позиции, плавную duration функцию, provider
  failures/ambiguity и Noize MC AcoustID regression. Перед переносом fixture
  проверить upstream license и происхождение. Не коммитить fixture с
  несовместимой лицензией/персональными данными: тогда сделать эквивалентный
  минимальный синтетический fixture и сослаться на upstream test как источник
  поведения.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> владелец подтвердил, что upstream — его личный код и лицензии допустимы;
> отдельный лицензионный блокер по fixtures снят. Логика реализуется
> самостоятельно на Go, Python upstream — только reference (см. A7).

## Цель, сущности и границы

После успешного source analysis оператор видит входящие группы файлов, для
которых поиск MusicBrainz и optional AcoustID даёт объяснимые release/recording
кандидаты и confidence. Группа с однозначными назначениями всех своих файлов
может автоматически получить согласованные локальные сущности и provider/source
links. Неоднозначные/недостаточные случаи остаются для ручного review. В ручном
экране можно выбрать edition либо локальный release, распределить файлы по
позициям, искать recordings, менять группу и подтвердить нужные связи. Никакое
действие страницы не меняет source files и не создаёт publication.

Группировка — производный read model текущих файлов и тегов, не подтверждённая
медиатека. Group identity должна переживать открытие страницы, но draft
назначения и drag/drop остаются только в page state. Подтверждённые local
entities/provider links/source assignments — текущее состояние, не журнал.
`media_variant` (SHA identity) не равен записи, релизу или позиции; один variant
может быть источником M:N для подтверждённых позиций. Не сливать разные provider
recording MBID из-за одинакового аудио.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> один variant — уникальный файл с возможными многими locations (M:N); без SHA
> variants независимы (B3).

## Контракт группировки и matching

### Ключи группы

Реализация должна сначала представить нормализованные tag values и их
provenance/missing/conflict отдельно, затем применить последовательность:

1. Валидный release MBID из утверждённых тегов имеет приоритет группировки;
   MBID scope может объединить файлы из разных folders и roots. Если тот же
   MBID соседствует с конфликтующими заполненными release identity fields,
   конфликт не скрывать: отделить конфликтные входы и показать оператору.
2. Без usable release MBID использовать полный `ALBUM` + полный упорядоченный
   `ALBUMARTIST`; если `ALBUMARTIST` отсутствует, fallback к полному `ARTIST`.
   DATE/CATALOGNUMBER учитываются, когда присутствуют; country из explicit
   tags либо из разрешённого release MBID также разделяет edition. Не дополнять
   missing field значением другой записи. Конфликт заполненных полей формирует
   отдельные группы.
3. Папка — fallback для отсутствующих/недостаточных edition keys. При
   недостаточной edition-информации одинаковые теги в разных папках не дают
   права их склеить. При достаточной согласованной edition-информации совпадение
   группировки возможно между папками и roots. Правила достаточности не могут
   быть выведены из эвристического порога matching: если контракт не даёт
   детерминированно решить конкретное сочетание missing/conflicting values,
   включить этот кейс как decision gate до автоматического merge, не придумать
   default.
4. `DISCNUMBER` никогда не является partition key. Его значение — локальный
   evidence позиции/носителя при assignment. Наличие нескольких дисков сохраняет
   один альбом.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> country как строки сравниваются тем же утверждённым fuzzy scoring, без карты
> код↔название на этом этапе; конфликт только по country не добавляет отдельный
> veto/review, прежнее разделение по country сохраняется (B2). Валидные embedded
> MBIDs: невалидные игнорируются с diagnostics, повтор одного ID дедуплицируется,
> несколько различных валидных ID — unresolved, первый не выбирать (B4).

Merge/split/move — явные ручные операции над текущим derived grouping. Они не
переписывают исходные теги и не подменяют MBID. Подтверждённые entity links
сохраняются. Решение не устанавливает, являются ли ручные corrections групп
немедленно сохранённым операторским состоянием или page-only drafts; до API/DB
design выяснить у владельца lifecycle этих именно group corrections. Не выводить
их persistence из правила page-only assignment drafts. Удалённые/изменившиеся
files не остаются assignment targets.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> lifecycle group corrections закрыт: page drafts до отдельного явного confirm,
> затем сохранение в DB.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> изменившийся файл сбрасывает group correction, неизменившийся сохраняет; новый
> файл не присоединяется автоматически к manual-группе (B5).

**Обязательный exact-normalization gate:** теги хранятся как массивы строк
(`observed_tags` из plan 10); нельзя делить значение по `;` или `/`, сворачивать
мультиартистов к первому имени или сравнивать только один из конфликтующих
values. Сопоставление полного ALBUMARTIST/ARTIST и обработка разного порядка,
повторов, split tag arrays и пустых строк должны быть специфицированы и
покрыты fixtures до группировочной миграции. Перенести upstream text
normalization для score в точности. Если по имеющимся решениям невозможно
однозначно определить продуктовую эквивалентность списков тегов, это реальный
блокер группировочного auto-merge: запросить владельца, а не записать в этом
плане NFKC/case/separator/order default.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> normalization закрыта: NFC, case insensitive, trim крайних пробелов, пустые
> элементы исключаются, порядок и повторы сохраняются, значение не делится по
> `/` или `;`.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> дубликаты значений могут дедуплицироваться в set со стабильным порядком «первое
> вхождение»; сортировка не вводится. `ARTIST=artist1`/`ARTIST=artist2` — список,
> а не конфликт (B1 переопределяет сохранение повторов из A3).

### Поиск candidates и evidence

- Для каждого eligible audio file сформировать lookup request из source
  observed tags, duration, available position/total, embedded MBIDs, fingerprint
  и identity текущего analysis + source location. SHA256 не является
  обязательным prerequisite и может быть отключён/NULL: identity и fencing
  опираются на current analysis identity, source-root/location/variant identity
  и observed tags, а не на обязательный hash. Не дополнять отсутствующий тег
  provider-значением перед скорингом.
- MusicBrainz используется для release/recording data и authoritative local
  cache; AcoustID optional lookup выдаёт recording evidence и само по себе не
  выбирает release. Если AcoustID выключен/не настроен/не доступен, его factor
  отсутствует (не «совпал», не штраф). Provider failure не меняет
  `media_variant.problem_flags` и не удаляет уже сохранённый cache.
- Candidate track assignment строится для реальных позиций release с совпадающим
  recording evidence и доступными title/artist/duration/position features.
  Сохранить full evidence: raw/source/provider values, normalized values,
  available/not-applicable/missing, factor weights/contributions, provider
  provenance, policy version, вычисленный confidence и причины conflict/veto.
  Confidence `[0,1]` не объявлять калиброванной вероятностью; подтверждённая
  связь confidence не хранит.
- Embedded release/recording/track MBID — сильное сопоставимое evidence общей
  формулы, но сам по себе не снимает конфликты и не превращает candidate score
  в 1. Существующий manual link не перебирается автоматикой. Ручной выбор не
  меняет score кандидатов.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> embedded MBID — только lookup/evidence, без числового веса и без forced 1;
> требование «участвует в общей matching-формуле» superseded (B6).

- Считать group release candidate qualified, только если он присутствует и
  score >= 0.70 **для каждого файла группы**. На пересечении qualified release
  candidates считать сумму scores по всем файлам. Принять release автоматически
  только при одном уникальном максимуме; при равенстве — ручной выбор, в том
  числе когда MBID lexical order мог бы сделать выбор детерминированным. Gap
  winner-runner-up не считать.
- Строго отличать **утверждённый** выбор release (уникальный максимум суммы
  scores среди qualified release candidates; иначе review) от **неутверждённой**
  раскладки файлов по позициям. Механизм position assignment — «ровно одно
  допустимое assignment по порогу 0.70», разрешение коллизий (один файл / одна
  provider position) и tie/equal-max раскладка — не является одобренным
  продуктовым решением и не выводится из scoring или полноты группы. До
  отдельного явного одобрения владельцем это **блокирующий owner gate для M07**;
  новый global algorithm не выбирать, default не предполагать, а раскладка
  остаётся read-only/review.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> owner gate M07 закрыт: assignment — взаимно-однозначная раскладка с максимальной
> суммой confidence, каждый файл >= 0.70; при нескольких равных лучших раскладках
> вся группа уходит в review.

- Переносимый approved-минимум после выбора release: для auto-apply группы
  **каждый файл** группы должен получить назначение; отсутствие назначения хотя
  бы одного файла отправляет всю группу в review, и уверенная часть не
  применяется как auto outcome. Конфликтные manual links блокируют auto-apply.
  Повторное использование/нестандартное M:N создаётся только явным ручным
  решением с сохранением model constraints.
- Неполный provider track list coverage разрешён: не требуется назначить все
  provider positions, и непокрытые позиции не блокируют auto outcome, если все
  входные файлы группы однозначно назначены. Это НЕ разрешает частичное auto
  assignment файлов группы.
- Порог 0.70 применить к release qualified score (а к assignment score — owner
  gate M07 закрыт 2026-10-09, см.
  [Приложение A](#appendix-a-owner-decisions-2026-10-09)) именно в одобренных
  точках. Не выдумывать
  второй порог, gap, confidence/status enum, комбинацию причины или статус
  только ради UI. Policy version версионирует
  формулу/принятие; обновление policy заменяет текущие candidates/automatic
  links согласно owner contract, но manual links защищены.

### Ручное review и сохранение

- S02 входящие: таблица групп, unique files/paths, свободные files,
  назначено/всего входных файлов, candidates/причина review, состояние source и
  provider. Не путать число позиций издания с полнотой входной группы.
- S03 matching: текстовый поиск candidates; выбранный edition и его полные
  media/track positions; отдельный file pool; candidate evidence inspector;
  drag/drop и keyboard assignment, unassign/replace, явный dialog для
  over-occupied row; ручные merge/split/move groups. Предусмотреть local release
  без provider edition и recording lookup в чужих releases как provenance,
  сохраняя локальные album/title/artist/order.
- Draft assignments независимы для каждого выбранного target release и остаются
  только в активном page session. Навигация/refresh не обещает их сохранения.
  Не добавлять API autosave, draft tables или persistent drafts. Подтверждение
  отдельное и сохраняет только подтверждённые local entities, provider links и
  track sources. «Подтвердить» не означает «опубликовать».
- Перед подтверждением backend перепроверяет актуальную group/source identity,
  provider candidate/cache state и manual-link protections; устаревший candidate
  не превращается в подтверждённый link. При конфликте показать refresh/review,
  не терять операторский ввод внутри текущей страницы. Точная механизм/version
  fence — технический design этапа, не новый пользовательский status.
- UI confidence/evidence приходит только из backend; никаких fake fixture-level
  high/low scores и frontend-computed formula. Прототип остаётся reference,
  не production contract.

## Технические границы и исследование до реализации

- **Текущая карта реализации (baseline плана 10, по codegraph):** inventory
  orchestration/reconciliation находится в
  `backend/internal/service/source_roots.go`,
  `backend/internal/persistence/source_repository.go`,
  `source_candidates.go` и связанных scan apply paths; актуальные SHA/fingerprint
  work/step results — `backend/internal/persistence/media_variant.go`,
  `source_analysis_steps.go`, `backend/internal/jobs/source_analysis_worker.go`.
  MusicBrainz сейчас доступен как setup/config/connectivity boundary через
  `backend/internal/settings/settings.go`, `service/setup.go` и
  `backend/internal/api/setup.go`; это не release/recording matcher. Sources UI
  находится в `frontend/src/features/sources/`, навигация —
  `frontend/src/routes/AppShell.tsx`. Текущего входящих/matching API, production
  matching persistence/service или React matching screen в этих точках не
  обнаружено; не выдавать prototype и concept DBML за реализованные abstractions.
  Перед каждым increment повторно проверить текущие call sites и точные имена.
- Соблюдать technical layering: `api → service → persistence`; только
  `persistence` использует Bun/PostgreSQL. MusicBrainz/AcoustID HTTP и parsing
  находятся в `integrations`; service оркестрирует provider adapters; River
  args минимальны, durable worker перечитывает inputs из БД. Не добавлять env
  vars, публичный auth, CORS, второй ORM или новые внешние провайдеры.
- Прежде чем выбирать durable execution/cache/RPS contract, записать короткий
  технический decision appendix по официальным docs: MusicBrainz WS/2 rate
  policy, User-Agent, query/lookup/browse include/limits/pagination/error
  contract и self-hosted endpoint assumptions; AcoustID API auth, fingerprint
  lookup semantics, rate guidance and optionality; official Go HTTP client
  cancellation/timeout/retry semantics used by repo; River transaction,
  uniqueness, retry/recovery/admission conventions already in code; Postgres
  locking/advisory lock policy already used in repo. Хранить URL/version/access
  date и distinguishing fact vs proposal. Никаких invented RPS defaults: если
  официальная policy требует ограничение, до включения provider queries выбрать
  технический mechanism, не смешивая его с approved file concurrency=4.
- Уточнение владельца после составления плана: локальный self-hosted
  MusicBrainz должен поддерживать запросы без искусственного rate limit;
  речь об ограничении частоты, а не об отключении HTTP timeout. Существующие
  mode/base URL переиспользовать. Предусмотреть возможность отключить rate
  limit для self-hosted, не распространяя public MusicBrainz policy на локальный
  endpoint и не снимая обязательные ограничения публичного MusicBrainz или
  AcoustID. Способ управления, значения по умолчанию и сохранение настройки
  согласовать с владельцем до реализации; новые env vars не добавлять.
  В M05 проверить отсутствие искусственной паузы для self-hosted при отключённом
  лимите и соблюдение public policy; HTTP timeout этим решением не меняется.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> отдельный toggle self-hosted, default off; при включении задержка между
> запросами (секунды, float `0.0..60.0`), начальное/дефолтное значение поля —
> 0.5 s; при off — запросы без искусственных пауз; HTTP timeout и public policy
> сохраняются.

- Изучить текущие provider/settings/API abstractions и avoid second matching
  pipeline. Объём provider response/cache freshness needs technical design;
  не создавать неподтверждённую долгую provider snapshot history или скрытый
  timer. Повторный match — explicit/current request или одобренный lifecycle,
  его автоматическое расписание не добавляется.
- Matching group apply + local entities/links должны быть атомарными на уровне
  PostgreSQL. Задача, если provider fetch асинхронен, передаёт только operation
  ID и typed intent; current source/provider/config прочитывается из БД. Зафиксировать
  stale fences по source stat identity (`size`/`mtime` согласно решению владельца),
  source-root/location/variant identity, provider cache candidate identity и
  matching policy version. Не использовать stale delivery для overwrite manual
  links.
- Определить idempotency key и сериализацию двух конкурентных group applies,
  manual edits и rescans. Повторная доставка/retry не создаёт дубли local
  entities/links и не заменяет более новое решение. DB locks/constraints, а не
  только in-memory mutex/UI disable, обеспечивают результат. Записать стабильный
  порядок row locks; транзакция failure не должна оставлять половину группы.
- Migration plan с down path и constraints обязателен для каждого физического
  изменения. До создания таблиц/колонок сопоставить актуальные физические
  миграции с conceptual `docs/design/music_ingest_redesign.dbml`; DBML не
  считать уже реализованной схемой. Старые publications/source state не
  затрагивать. Проверить populated upgrade/rollback и сохранение неизвестных
  historical values.
- Huma/OpenAPI — API source of truth; frontend client/hooks/mock только через
  `task generate`, никогда вручную. UI — React features/router, не прототипный
  `docs/app-design/` и не Operations screen.

## Последовательные implementation increments

Каждый increment — небольшой reviewable change с самостоятельным acceptance;
границы/имена файлов уточнить по актуальному codebase перед работой. Таблица —
последовательность, не требование одного гигантского PR.

| Этап | Scope и основные точки | Зависимости / безопасное промежуточное состояние | Acceptance |
| --- | --- | --- | --- |
| M01 — baseline / upstream semantics | Зафиксировать ревизию baseline; сверить `backend/internal/integrations/`, `service/`, `persistence/`, API, current migrations, frontend routes; оформить [provider/River official-doc research appendix](../11-m01-provider-and-river-research-2026-10-09.md). Собрать license/provenance inventory upstream fixtures. | Нет. Только документационное/техническое исследование; matching behavior не меняется. | Appendix содержит официальные источники, даты и подтверждённые лимиты/операционные facts; upstream clone/commit и fixture licensing записаны; no code. |
| M02 — scoring module + regression corpus | Новый чистый service-domain scoring implementation, typed inputs/evidence/policy version и deterministic table fixtures. Перенести upstream formula weights/normalization/duration и coverage cases; интеграция AcoustID как optional factor; не переносить explicit-ID forced 1. | M01. Pure matching score не записывает links и не вызывает сеть. | Golden results по upstream fixtures; available-only weighted average, lower clamp, fuzz/transliteration, all position factors, exact duration values, missing factors, explicit IDs regular formula; Noize fixture license handled; full `task verify`. |
| M03 — exact tag values + grouping model | Отдельные normalizer/group-key parser tests; продуктовый gate на полный multi-value semantics и persistence lifecycle ручных merge/split/move; derived grouping read model с cross-root support, MBID/tag/folder priorities, country/date/catalog conflicts, DISCNUMBER. | M02 плюс required tag semantics и group-correction lifecycle. Пока exact product equivalence не решена, допускается только safe unmerged conflicts, не автоматический merge. | Fixtures доказывают MBID cross-root grouping; full ALBUMARTIST/fallback ARTIST; DATE/CATALOGNUMBER/country splits; conflicting and missing keys don't silently join; disc number doesn't partition; same insufficient tags in different folders stay separate; explicit merge/split/move с одобренным lifecycle. |
| M04 — physical grouping/matching schema | Предложение и review схемы; versioned migrations/models/repositories для persisted provider cache, current candidates/evidence, confirmed entity/provider/source links и group identity/fences только если lifecycle group corrections требует хранения. Constraints/FK/idempotency/manual protection. Down миграции. | M01–M03, migration review. Не добавлять assignment drafts. | PostgreSQL integration tests clean/populated up/down; guards for provider identity, candidate confidence/policy, manual links; identity/analysis fencing valid with SHA disabled/NULL (no hash prerequisite); repeated apply idempotent; rollback preserves preexisting inventory/analysis and no publication rows are created. |
| M05 — providers + evidence cache | MusicBrainz WS/2 adapter/read model, AcoustID optional adapter; provider-specific types stay integrations. Durable cache/update rules, official-compliant rate limiter/semaphore only after M01 research; query batching/pagination and typed failures. | M01 and schema decisions M04; scoring/group interfaces M02–03. No matching decision yet. | Fixture-only HTTP tests: public and self-hosted base URL, configured User-Agent, query/lookup and release enrichment, pagination/duplicates, malformed/timeout/rate-limited/unavailable, cache reuse/refresh, AcoustID off/on/no-key/error; no external network in local tests; no unapproved RPS/env. |
| M06 — deterministic candidate generation and group selection | Service orchestration over current file-group members + cached/fetched candidates; compute candidate evidence and release aggregate. Replace candidates transactionally, preserve manual links. | M02–05; M06–M08 блокируются открытым owner gate trigger/lifecycle auto matching (после анализа vs command), default не выбирать. Before accepting auto decisions, full group no-side-effect simulation/read model works. | Tests for release intersection, per-file >=0.70 qualification, unique max sum, tied maxima require review, no winner if any file lacks qualified candidate, no extra gap, candidate evidence complete. One group resolves across folders/roots under grouping rules. |
| M07 — track assignment and atomic confirmation | **Блокируется owner gate position assignment coordination (однозначность/коллизии/tie — не approved).** После закрытия gate: build per-file→provider-position assignments; create/reuse local entities and confirmed links atomically for the whole eligible group. Add manual explicit M:N actions and manual-link guard. Apply all source files or none for automatic group. | M04–06 плюс закрытый owner gate position assignment. Auto-selection read-only until atomic write gate is ready. | После approval gate: тесты раскладки по утверждённому решению; all source files required и partial auto apply запрещён (all files, not all tracks); no all-release-position coverage requirement; manual links survive rerun; manual explicit M:N; transaction rollback; idempotent delivery; stale source/provider/policy fence; concurrent manual edit/rescan/apply serialization. |
| M08 — REST/OpenAPI/generated client | Huma APIs for incoming groups, candidate evidence/search, matching read models, explicit confirmation and manual group/assignment actions. Exact endpoint/DTO names resolved after schema; ownership and group/version fences server-side. `task generate`. | M03–07 API shapes and transaction semantics stable. | Huma/API tests for 200/202 only if async contract chooses it, missing/foreign IDs, conflicts/stale fences/provider outage, manual protection and transactional errors; generated contract drift clean. No custom publication endpoints. |
| M09 — Incoming + matching UI | React route/feature for incoming group list and S02/S03 matching, page-local drafts, accessible search/assignment/evidence inspector/group corrections. Add explicit manual confirm; no publication action. | M08. Prototype `docs/app-design/01-product-and-flows.md`, `02-screens.md`, `03-data-and-evidence.md` are references only. | RTL tests for initial/error/empty/provider unavailable, evidence and missing/not-applicable, candidate changes, independent drafts per release, drag/keyboard, occupied row, manual merge/split/move, manual link protected, stale conflict retaining page draft, navigating/reloading does not claim draft persistence; no fake score. Browser review desktop/mobile-width and both themes, focus/keyboard/readable dense tables. |
| M10 — end-to-end acceptance / independent review | Update only stale **status** notes with dated addenda (do not rewrite historical report facts); reconcile README links and docs status after evidence. Run whole story with PostgreSQL/River fixtures and providers mocked; provide independent review. | M01–09 complete. | `task verify`; CI checks for changed Go/API/schema/client/platform surfaces; independent reviewer maps decisions, migrations, concurrency and acceptance. No network-dependent acceptance or publication claim. Move plan to `done/` only after accepted complete evidence. |

> **Уточнение 2026-10-09:** блокирующие owner gates, упомянутые в M03, M06 и M07
> (exact tag semantics, group-correction lifecycle, trigger/lifecycle auto
> matching, position assignment coordination), закрыты в
> [Приложении A](#appendix-a-owner-decisions-2026-10-09); настройка limiter
> self-hosted уточнена в A6, upstream licensing/reference — в A7. Строки таблицы
> сохранены как состояние на дату подготовки.

## Cross-stage safety and acceptance invariants

1. **Atomic group outcome:** the automatic action is group-wide over every
   incoming file. No high-confidence subset can auto-apply while the rest enters
   review. Provider-side release positions absent from the provider response are
   not required matches.
2. **No fabricated assignment/reuse:** automatic match cannot silently reuse
   source or target; explicit manual action may represent the model's allowed M:N
   semantics. Требование «each file requires its own eligible, unique target
   position» относится к неутверждённой position-assignment логике и действует
   только после закрытия owner gate M07; до этого раскладка read-only/review.
   owner gate M07 закрыт 2026-10-09 (см.
   [Приложение A](#appendix-a-owner-decisions-2026-10-09)).
3. **Missing vs mismatch:** unavailable evidence is excluded from the weighted
   denominator; present conflicting values are compared by the approved scoring
   factors and must not be reclassified as missing. Do not add a special conflict
   veto/penalty beyond the approved formula. Never convert missing into equality
   or empty into an implied fallback beyond the explicit grouping rule. Any
   not-yet-specified product treatment must block affected auto merge/decision.
4. **Manual precedence:** manual provider/entity/source decisions cannot be
   overwritten by matching policy refresh, stale cache, duplicate delivery or
   concurrent worker. Operator must explicitly change/unlink or return to
   automatic mode.
5. **Source/provider fences:** verify current size/mtime and IDs immediately
   before persisted application. Old worker cannot apply after source mutation,
   group membership change, newer provider evidence or new policy. Follow the
   owner-approved product freshness criterion; stricter race/security mechanics
   may be implementation safeguards, not added product acceptance.
6. **Provider resilience:** typed no-match/ambiguous/rate-limit/unavailable/
   malformed outcomes preserve prior provider cache where appropriate and remain
   distinct from source analysis problems. Optional AcoustID absent = no factor.
7. **DB integrity:** provider release/recording/track records belong to same
   provider; local entities are independent; one provider track is not linked to
   multiple local tracks; local variant→tracks remains supported M:N. Confidence
   belongs to current candidates only; confirmed link stores method and automatic
   policy version, no score.
8. **No publication coupling:** no file write/copy/remux/tag write/output path
   operation, no publication row/attempt mutation and no deletion of any source.

## Verification policy

- Follow project gate: **only `task verify` as local test/build gate**. Do not run
  focused suites separately unless `task verify` fails and a narrow diagnostic is
  needed for that failure. Markdown-only plan authoring does not justify local
  app tests/builds.
- Each implementation increment documents exact revision and `task verify`
  outcome. PostgreSQL-dependent migrations/transactions/enqueue/recovery require
  real integration coverage per repository convention. GitHub CI platform matrix
  and reviews are CI-only; do not claim them from local checks.
- Use deterministic provider fixtures and controlled barriers; don't use sleeps,
  live public network, or mutable user media as test fixtures. Respect source
  read-only behavior. Matching tests cover exact transferred score outputs and
  grouping/assignment edge cases above.
- API changes regenerate through `task generate`; never hand-edit
  `frontend/src/api/generated/**`. Frontend tests must ensure no publication
  calls from matching flow.
- Independent reviewer receives plan, diff, exact revision, test gate output,
  migration up/down evidence, concurrency/idempotency cases and browser
  acceptance. Historical failures remain recorded as failures; add dated
  follow-up status rather than rewriting history.

## Остаточные вопросы / gates

Продуктовые решения о приоритете, grouping priority/segmentation, country,
manual merge/split/move, page-only draft, manual-link protection, AcoustID,
legacy scoring, score threshold, group release winner/tie и точном значении
«полного релиза» получены и не являются открытыми вопросами.

**Открытые блокирующие owner gates (не решены; default не предполагать):**

- **Trigger/lifecycle auto matching перед M06–M08:** не утверждено, запускается
  ли auto matching автоматически после успешного source analysis или только
  явной командой оператора. До явного одобрения владельцем jobs/API/trigger,
  обработка новых членов группы и later/late-completion analysis не
  реализуются; тесты на эти случаи добавляются только после approval. Не
  выбирать post-analysis-по-умолчанию либо command-по-умолчанию.
- **Position assignment coordination (до M07):** однозначность допустимого
  assignment, разрешение коллизий (один файл / одна provider position) и
  tie/equal-max раскладка не утверждены; не выводить их из scoring, полноты
  группы или MBID order и не выбирать новый global algorithm.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> оба перечисленных блокирующих owner gate закрыты: (1) matching запускается
> автоматически после успешного анализа, новые/поздно готовые файлы пересчитывают
> группу с сохранением manual protection; (2) position assignment — взаимно-
> однозначная раскладка с максимальной суммой confidence, каждый файл >= 0.70,
> равные лучшие раскладки уходят в review. Формулировки gates выше сохранены как
> состояние на дату подготовки.

Перед зависящими increments остаются **технические** gates: официальный research
MusicBrainz/AcoustID rate/HTTP policy и выбранный compliant runtime mechanism;
fixture licensing/provenance; physical schema / migration and lock review; точное
значение multi-value artist/tag equivalence при группировке; lifecycle
сохранения ручных merge/split/move. Эти пункты являются продуктовыми blockers,
если нельзя вывести детерминированное поведение из принятых решений и
observed-tags contract; в таком случае запросить владельца до auto merge/API/DB,
не предполагать нормализацию или persistence. Остальные перечисленные gates —
технические. Кроме перечисленных открытых owner gates (trigger/lifecycle auto
matching, position assignment coordination) других неразрешённых owner decisions
этот план не фиксирует.

> **Уточнение 2026-10-09 (см. [Приложение A](#appendix-a-owner-decisions-2026-10-09)):**
> оба перечисленных owner gate закрыты (см. выше). Из «технических» gates
> продуктово решены exact multi-value tag equivalence при группировке (A3),
> edition-sufficiency без MBID (A4), lifecycle сохранения ручных merge/split/move
> (A5), управление self-hosted limiter (A6, отдельный toggle default off,
> задержка 0.0..60.0, начальное поле 0.5 s) и upstream licensing/provenance
> (A7, лицензии допустимы).
> Остальные перечисленные пункты остаются техническими до соответствующих
> increments.

## Связанные документы

- `docs/plans/done/10-staged-source-analysis.md` — завершённый источник SHA,
  ffprobe/fingerprint, source freshness и staged lifecycle; не matching.
- `docs/design/decisions.md` — authoritative matching confidence/links,
  provider and layering conventions.
- `docs/design/data-model.md` и `docs/design/music_ingest_redesign.dbml` —
  conceptual entities and constraints, not evidence of physical implementation.
- `docs/app-design/01-product-and-flows.md`, `02-screens.md`,
  `03-data-and-evidence.md` — UX/data reference; раннее «полный релиз» читать
  только согласно финальному уточнению владельца, приведённому выше.
- Upstream scoring reference: read-only clone/commit and tests documented in
  «Реестр одобренных решений и источников»; external source, not runtime
  dependency.

---

# Приложение A. План 11 — решения владельца по группировке и автоматическому matching (2026-10-09)

<a id="appendix-a-owner-decisions-2026-10-09"></a>

**Дата фиксации: 2026-10-09. Статус: явные продуктовые решения владельца.**
Это дополнение фиксирует решения по ранее открытым owner gates и не переписывает
исторические факты плана: формулировки выше сохранены как состояние на дату
подготовки. При конфликте между этим приложением и телом плана или более ранними
формулировками приоритет имеют решения ниже. Реализация этим приложением не
заявляется.

## A1. Триггер и lifecycle автоматического matching (закрывает owner gate M06–M08)

- Auto matching запускается автоматически после успешного завершения source
  analysis. Отдельная явная команда оператора как обязательное условие запуска
  не требуется.
- Новые файлы группы и файлы, ставшие пригодными позже (новые/late-completion
  analysis), пересчитывают группу. Пересчёт выполняется с соблюдением manual
  protection: подтверждённые manual links защищены и не перебираются автоматикой.
  Повторный расчёт не создаёт дубли local entities/links.

## A2. Position assignment coordination (закрывает owner gate M07)

- Assignment — инъективная раскладка всех входных файлов группы на различные
  provider-позиции, максимизирующая сумму confidence по раскладке. Неиспользованные
  provider-позиции допустимы: покрывать весь provider tracklist не требуется.
- В раскладке каждый назначенный файл должен иметь confidence >= 0.70.
- При нескольких равных лучших раскладках (одинаковая максимальная сумма
  confidence) автоматический выбор не делается: вся группа уходит в ручной
  review. Детерминированный тай-брейкер (в том числе lexical MBID order) не
  вводится.

## A3. Exact normalization тегов для группировки (закрывает multi-value / exact-normalization gate)

- Значения тегов сравниваются после: Unicode NFC; без учёта регистра (case
  insensitive); trim только крайних пробелов.
- Пустые элементы исключаются из сравниваемого списка.
- Порядок элементов и повторы сохраняются: значения не сортируются и не
  дедуплицируются.
- Значение не делится по `/` или `;`; split tag arrays не сворачиваются к одному
  имени.
- Это группировочное сравнение отдельно от переносимой upstream text
  normalization для score; последняя переносится точно, как задано в теле плана.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> дубликаты значений могут дедуплицироваться в set со стабильным порядком «первое
> вхождение»; сортировка не вводится. Повторяющиеся теги вида
> `ARTIST=artist1`/`ARTIST=artist2` — список, не конфликт (B1).

## A4. Достаточность edition-ключа без MBID (закрывает edition-sufficiency gate)

- Без usable release MBID: полный `ALBUM` вместе с полным artist (полный
  `ALBUMARTIST`, иначе fallback к полному `ARTIST`) и `DATE` **или**
  `CATALOGNUMBER` достаточно, чтобы группировать файлы между folders и roots.
- Missing и filled различаются: отсутствующее значение не приравнивается к
  заполненному и не присоединяется к нему.
- `country` (явные теги либо выводимая из разрешённого release MBID) остаётся
  разделяющим признаком: разные страны — разные группы.

## A5. Lifecycle ручных group corrections (закрывает group-correction lifecycle gate)

- Ручные merge/split/move groups живут как page drafts до отдельного явного
  confirm; навигация/refresh сохранение draft не обещает.
- После отдельного явного confirm corrections сохраняются в DB (persisted).
- Это не меняет правило page-only assignment drafts и не делает «Подтвердить»
  публикацией.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> изменившийся файл сбрасывает group correction, неизменившийся сохраняет; новый
> файл не присоединяется автоматически к manual-группе (B5).

## A6. Self-hosted MusicBrainz limiter — toggle и задержка

- Отдельный toggle для self-hosted MusicBrainz — runtime-настройка в БД с UI
  toggle; значение по умолчанию — **off**.
- При включённом toggle настройка задаёт **задержку между запросами в секундах**
  (`float`, диапазон `0.0..60.0`); начальное/дефолтное значение поля —
  **0.5 секунды**.
- При off возможность запросов без искусственных пауз сохраняется.
- HTTP timeout сохраняется; обязательные ограничения публичного MusicBrainz и
  AcoustID сохраняются; новые env vars не добавляются.

## A7. Upstream rights и способ реализации

- Владелец подтвердил, что upstream — его личный код; лицензии на перенос
  fixtures/семантики допустимы, отдельный лицензионный блокер по происхождению
  fixtures снят.
- Логику реализуем самостоятельно на Go; Python-реализация upstream — только
  reference для переносимой семантики, не runtime-зависимость и не копируемая
  реализация.

## A8. Сохранность прочих контрактов

Все остальные контракты плана без изменений: публикация вне scope, source
read-only, manual protection, provider/rate/layering, схема/миграции, fencing,
idempotency и acceptance invariants. Это приложение не является доказательством
выполнения этапов M01–M10.

---

# Приложение B. План 11 — позднейшие решения владельца (2026-10-09, позже)

<a id="appendix-b-owner-decisions-late-2026-10-09"></a>

**Дата фиксации: 2026-10-09 (позднейшее уточнение того же дня). Статус: явные
продуктовые решения владельца.** Приложение не переписывает исторические факты и
текст Приложения A; при конфликте приоритет имеют решения ниже. В частности, B1
переопределяет сохранение повторов из A3, а B6 supersedes прежнее требование
«встроенный MBID участвует в общей matching-формуле». Реализация не заявляется.

## B1. Теги latest: сохранение, списки и дубликаты

- Сохраняются все исходные теги; observed tags нельзя подменять или отбрасывать.
- Повторяющиеся теги (например `ARTIST=artist1` и `ARTIST=artist2`) — это список,
  а не конфликт; мультиартисты не сворачиваются к первому имени.
- Дубликаты значений могут дедуплицироваться в set со стабильным порядком
  «первое вхождение»; сортировка не вводится, порядок консистентен прежнему
  порядку, если владелец не скажет иначе. Это переопределяет сохранение повторов
  из [A3](#appendix-a-owner-decisions-2026-10-09).
- Форматы и alias mapping table подлежат изучению; разумная реализация
  допускается (владелец: «в целом пока делай как-то, потом поправим»). Нельзя
  утверждать, что ffprobe сохраняет все теги, без отдельного исследования.

## B2. Country: сравнение строк

- Строки country сравниваются тем же утверждённым fuzzy scoring; отдельная карта
  код↔название на этом этапе не вводится.
- Конфликт исключительно по country не добавляет отдельный veto/review.
- Существующее разделение групп по различию country сохраняется, пока не
  противоречит сказанному; новый разделяющий или неразделяющий признак не
  изобретается.

## B3. media_variant, locations и независимость без SHA

- Один `media_variant` — уникальный файл, у которого может быть много locations
  (M:N); прежнее «один variant — источник M:N» сохраняется.
- Без SHA variants независимы: отсутствие hash не делает их одним файлом и не
  является обязательным prerequisite identity.

## B4. Embedded MBIDs: валидность и коллизии

- Невалидные embedded MBIDs игнорируются с diagnostics.
- Повторяющийся один и тот же ID дедуплицируется.
- Несколько различных валидных ID — unresolved: не выбирать первый. Действует
  существующий выбор владельца «Учитывать валидные».

## B5. Group corrections и изменения файла

- Изменившийся файл сбрасывает group correction.
- Неизменившийся файл сохраняет correction.
- Новый файл не присоединяется автоматически к manual-группе.

## B6. Embedded MBIDs в matching: только lookup/evidence

- Позднейшее решение владельца: MBID используются только для lookup/evidence;
  числового веса они не несут и forced `1` не дают.
- Это supersedes прежнее требование «встроенный MBID участвует в общей matching-
  формуле» (реестр решений и body «Embedded release/recording/track MBID —
  сильное сопоставимое evidence общей формулы»).
- Manual-link protection и запрет фальшивого confidence/score = 1 сохраняются.
