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
> lookup/evidence, без числового веса и без forced 1. Последние pointers — B11
> (Go-аналог вместо 1:1-переноса Python/RapidFuzz) и B12 (готовые Go-библиотеки
> вместо самописных решений).

> **Датированное дополнение 2026-10-10:** контракт application key AcoustID
> уточнён в [Приложении B](#appendix-b-owner-decisions-late-2026-10-09), B15:
> write-only настройка в PostgreSQL, отсутствие ключа = нет AcoustID lookup,
> отдельного enable/disable toggle нет; MusicBrainz matching остаётся
> работоспособным без ключа. Реализация provider foundation этим не заявляется.

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

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> перенос upstream text normalization/`token_set_ratio` не делается 1:1: пишем
> идиоматичный Go-аналог на нормальных Go-библиотеках, без Python-таблиц и без
> переноса алгоритма RapidFuzz целиком; bit-exact parity не является gate (B11).

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

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> «Перенести upstream text normalization для score в точности» superseded:
> идиоматичный Go-аналог, ожидаемое документированное отличие, не bit-exact (B11).

### Поиск candidates и evidence

- Для каждого eligible audio file сформировать lookup request из source
  observed tags, duration, available position/total, embedded MBIDs, fingerprint
  и identity текущего analysis + source location. SHA256 не является
  обязательным prerequisite и может быть отключён/NULL: identity и fencing
  опираются на current analysis identity, source-root/location/variant identity
  и observed tags, а не на обязательный hash. Не дополнять отсутствующий тег
  provider-значением перед скорингом.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> acquisition тегов/метаданных — часть анализа; matching читает только сохранённые
> analyses и не делает file I/O по исходным файлам (B10).

- MusicBrainz используется для release/recording data и authoritative local
  cache; AcoustID optional lookup выдаёт recording evidence и само по себе не
  выбирает release. Если AcoustID выключен/не настроен/не доступен, его factor
  отсутствует (не «совпал», не штраф). Provider failure не меняет
  `media_variant.problem_flags` и не удаляет уже сохранённый cache.

> **Датированное дополнение 2026-10-10 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09), B15):**
> отдельного AcoustID enable/disable toggle не вводится: application key —
> write-only настройка в PostgreSQL, его наличие включает lookup, отсутствие
> ключа = AcoustID lookup не выполняется. Формулировка «выключен/не настроен/не
> доступен» выше читается согласно B15; при отсутствии ключа MusicBrainz
> matching остаётся работоспособным (optional factor отсутствует).

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

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> DB-known source/analysis identity проверяется как fence; точный контракт source
> stat остаётся техническим и должен быть согласован; matching не делает file I/O.
> Не изобретать критерии ослабления fence и не добавлять default
> backfill/reanalysis; lifecycle обновления метаданных уже известной inventory
> остаётся нерешённым (B10).

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
| M01 — baseline / upstream semantics | Зафиксировать ревизию baseline; сверить `backend/internal/integrations/`, `service/`, `persistence/`, API, current migrations, frontend routes; оформить [provider/River official-doc research appendix](#appendix-c-m01-provider-and-river-research). Собрать license/provenance inventory upstream fixtures. | Нет. Только документационное/техническое исследование; matching behavior не меняется. | Appendix содержит официальные источники, даты и подтверждённые лимиты/операционные facts; upstream clone/commit и fixture licensing записаны; no code. |
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
> сохранены как состояние на дату подготовки. Уточнение B10: acquisition
> тегов/метаданных — часть анализа, matching не читает исходные файлы;
> «all unknown tags needs owner» — неверно, владелец хочет сохранять все теги.
> Уточнение B11: M02-acceptance «Golden results по upstream fixtures / exact
> transferred score outputs» больше не действует — Go-аналог, versioned и
> documented оценки с ожидаемым отличием, не bit-exact.

## Ход выполнения (2026-10-09)

- **M01 — завершён:** baseline, официальные MusicBrainz/AcoustID/Go/River/
  PostgreSQL facts и inventory upstream fixtures зафиксированы в
  [research appendix](#appendix-c-m01-provider-and-river-research).
  Независимое повторное ревью закрыло замечания по fixture provenance и
  PostgreSQL locking; matching implementation этим ревью не принимается.
  Документационное изменение сохранено в `2760df8`; полный pre-commit
  `task verify` прошёл. Исходный baseline `8c205db` также прошёл полный
  `task verify` перед первым документационным коммитом.
- **M02 — реализация завершена локально:** чистый Go scoring с policy
  `matching-go-v1`, готовыми `adrg/strutil`, `anyascii/go` и `x/text` согласно
  B11–B12. Release/recording/assignment profiles, available-only denominator,
  negative duration contribution/lower clamp, legacy rounding, position factors,
  optional AcoustID и lookup-only MBID покрыты regression tests. Raw arrays
  сохраняются отдельно от дедуплицированного сравнения. Python-derived таблицы,
  генератор и самописный fuzzy алгоритм удалены до коммита реализации.
  Первоначальный `task verify` выявил устаревший expected value для Unicode
  case folding (`Straße`); тест исправлен на `strasse`. Независимое ревью также
  выявило и закрыло валидацию duration factor >1. Повторный полный
  `task verify` прошёл: Go integration, 241 frontend tests, 4 tools tests,
  generation, lint и build. Notice bundle включён в локальный build и Dockerfile;
  Docker/native platform CI локально не запускались и не заявляются.
  База изменения — `2760df8`; точная ревизия реализации фиксируется коммитом.
- **M03–M10 — ещё не завершены.** План остаётся в `todo/` до полной
  согласованной приёмки; чистый scorer пока не подключён к providers/БД/UI.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09), B14):**
> M03 в части **чистого grouping/reader capability** завершён и проверен полным
> `task verify`. Это **не** завершение всего плана: engine, БД, providers,
> автоматический matching и UI не реализованы; план остаётся в `todo/`.
> Историческая запись «M03–M10 ещё не завершены» выше сохранена как состояние на
> дату подготовки.

> **Датированное дополнение 2026-10-09 (четвёртый metadata-шаг engine, B13/B14):**
> Четвёртый независимый шаг source-analysis engine — извлечение тегов через
> TagLib (`go-taglib`) рядом с SHA/ffprobe/fingerprint — подключён к
> существующему prepared-scan lifecycle и проверен полным `task verify`
> (Go integration, 243 frontend tests, 4 tools tests, generation, lint, build);
> финальное независимое ревью закрыто, коммит готов. Metadata admitted/queued как
> обычный шаг: enumeration/prepared scan переиспользует тот же prepared input,
> второй pipeline не создаётся; шаг не требует managed tools (пустой tool
> selection, как у SHA256); ошибка metadata сохраняет успехи соседних шагов,
> retry повторяет только ошибочный шаг; перед запуском reader повторно
> валидируется prepared path (path pre/post validation), snapshots сохраняются.
> MKA: sole audio track, union global + soleAudio track tags, UID 0 = all,
> multi-audio не смешивается; ffprobe остаётся техническим probe. Канонический
> metadata result хранит один current row на SHA: latest `observed_at` (UTC,
> microsecond) побеждает, при равенстве — стабильный `winning_result_id`;
> canonical IDs стабильны; promotion работает при SHA off/digestless;
> проигравшие/неиспользуемые candidates очищаются. API/UI перегенерированы
> текущими (`task generate`). Это **не** завершение всего плана: M03 остаются
> БД/manual corrections/grouping persisted/feeds и прочее, M04–M10 не
> реализованы; план остаётся в `todo/`. База изменения — `1a52919`; точная
> ревизия реализации фиксируется коммитом.

> **Датированное дополнение 2026-10-09 (M04a — schema/persistence/service
> grouping, bounded):** Зафиксирован первый ограниченный increment M04 —
> grouping schema/persistence/service. Физическая схема и versioned migrations
> `20261029000000_incoming_grouping` с проверяемым down path:
> `incoming_grouping_state` (singleton с `revision`/`needs_refresh`),
> `incoming_group`, `incoming_group_member` (`ready`) и
> `incoming_group_member_location` — намеренно **скопированные** fence-данные,
> а не ссылки, чтобы retirement inventory/work не блокировался persisted
> corrections. Persistence `IncomingGroupingRepository` и service
> `IncomingGroups` дают stateless snapshot, refresh и явный confirm; page
> drafts merge/split/move остаются только в page state и в БД не пишутся.
> Manual confirmed corrections применяются CAS: confirm применяет draft, только
> если base- и draft-ревизии совпадают со свежим снимком БД, взятым под
> lock-ами; иначе `ErrIncomingGroupingConflict`. Свежий snapshot берётся под
> каноническим source mutation gate (`lockSourceFingerprintMutations`, advisory
> xact lock, namespace `MTRV`, key 4), затем row-locks
> `source_root → source_location → source_analysis_work` по id и
> `incoming_grouping_state`. Ready/unready manual retention: manual membership
> сохраняется только для файлов с пережившей точной location/stat identity;
> unready-члены сохраняются как `ready=false`; изменившийся файл сбрасывает
> correction, новый файл не присоединяется к manual-группе автоматически.
> Capture winner fences: identity capture использует metadata winner
> (`winning_result_id`), `observed_at`, provenance, tags и опциональный SHA256
> (`omitempty`), поэтому SHA не является prerequisite (работает при SHA
> off/NULL, `variant_id` падает на work/location id); technical identity
> включает probe/fingerprint winner и algorithm namespace/id. Durable
> `needs_refresh` hooks: `InvalidateIncomingGrouping` вызывается в той же
> транзакции, что и mutation, в analysis/inventory/root paths (enumeration
> apply, SHA/probe/fingerprint/metadata apply, fingerprint/probe reuse, source
> root update/delete, source scan apply) и при re-queue ранее succeeded шага.
> `task verify` прошёл 2026-10-09 23:10: Go integration, 243 frontend tests,
> 4 tools tests, generation, lint, build; независимое финальное ревью закрыто.
> Границы: API/startup/worker refresh ещё **не подключены**; следующий
> increment и provider cache/local/automatic matching (M04b и далее) **не
> завершены**; план остаётся в `todo/`. Публикация вне scope. База изменения —
> `bd50439`; точная ревизия реализации фиксируется коммитом (без выдуманного
> hash). Новых standalone research-файлов не добавлялось.

### Дополнение к прогрессу — 2026-10-10

- Grouping API и lifecycle подключены: snapshot, stateless preview и confirm
  повторяют действия на сервере; stale fences дают 409, invalid edits — 422.
  Startup восстанавливает refresh по durable marker; scan и analysis workers
  вызывают refresh после successful/failed settlement и recovery. Ошибка refresh
  не отменяет committed settlement; marker остаётся для повторного refresh.
- API regression tests проверяют непустые merge/split/move, отсутствие записи
  manual draft при preview и stale confirmation на fake store. Прямой
  PostgreSQL API cycle этими тестами не заявляется; persistence отдельно имеет
  PostgreSQL integration coverage.
- Контракт B15 реализован: AcoustID application key хранится в PostgreSQL,
  PUT/DELETE меняют ключ, ответы показывают только presence boolean. Password
  field пуст при загрузке и очищается после успеха. Endpoint-scoped redaction
  исключает echo ключа из framework 4xx errors; oversized/wrong-type inputs
  покрыты тестами. Lookup ещё не подключён.
- Provider cache foundation: migration 300, provider-scoped stable IDs,
  composite FKs, ordered repeated credits, numerical/display track positions,
  generation/configuration CAS и last-success JSON. Partial updates сохраняют
  omitted metadata/raw fields, explicit `{}` заменяет их. Track swaps,
  removal/reintroduction сохраняют IDs. HTTP clients, TTL и automatic refresh
  policy в этот increment не входят.
- Полный `task verify` 2026-10-10 00:26 прошёл: Go integration, 247 frontend
  tests, 4 tools tests, generation, lint и build. Независимые ревью этих
  bounded increments закрыты. База — `7eff040`; ревизии фиксируются коммитами.
- Весь план **не завершён**: local entities/links, candidates, provider
  execution, automatic matching и incoming UI остаются следующими increments.

## Cross-stage safety and acceptance invariants

### Provider adapters — проверенный bounded increment

**Normalization / explicit fetch-cache increment (2026-10-10):** source-scoped
current projections, stable provider/entity identities, endpoint switchback,
configuration fences и global fetch ordering реализованы в migration 310 и
repository/service. Authority и accepted order сохраняются по полям и
коллекциям; omitted track listings не превращаются в explicit empty lists.
MusicBrainz response envelopes сохраняются, explicit HTTP выполняется вне DB
transactions. Полный `task verify` 01:49 прошёл; независимое ревью закрыто.
Это bounded capability: durable provider jobs, automatic candidate selection,
local links и полный matching pipeline ещё не реализованы.

**Приоритет provider evidence — решение владельца (2026-10-10):** подробный
lookup приоритетнее search для полученных полей. Search может заполнять
отсутствующие поля, но не затирает поля lookup. Новый lookup заменяет старый;
omitted fields сохраняются.

**Reuse/refresh evidence — решение владельца (2026-10-10):** при первом поиске
получаем отсутствующие ответы; сохранённые успешные ответы переиспользуем без
TTL. Обновление выполняется по явному действию «Обновить кандидатов».
Ошибка обновления не меняет отношение к сохранённым успешным данным:
прежние кандидаты остаются видимыми и доступными для automatic matching на
обычных условиях. Предложение запрещать новый automatic apply только из-за
ошибки refresh владельцем отвергнуто. Остальные source/configuration fences и
защита manual links сохраняются.

Следующий composition increment подключает общий MusicBrainz request gate к
connectivity checker и provider factory, общий AcoustID limiter (3 requests/s)
и сохранённые self-hosted controls: toggle default off, delay 0–60 seconds,
initial value 0.5. Partial updates атомарно сохраняют omitted fields;
provider construction не меняет shared gate из устаревшего snapshot.
Canceled self-hosted waits не создают искусственный backlog. Полный
`task verify` 2026-10-10 01:03 прошёл (Go integration, 247 frontend tests,
4 tools tests, generation, lint, build). Provider jobs/cache mapping и
automatic matching по-прежнему остаются следующими increments.

MusicBrainz lookup/search реализованы через готовый
`go.uploadedlobster.com/musicbrainzws2 v0.19.0`, с bounded raw capture,
per-attempt limiter, protocol cooldown и cancellation-aware self-hosted limiter.
AcoustID использует узкий POST adapter: подходящего готового клиента с безопасным
POST и injected transport не найдено. Ключ не помещается в URL; отсутствие ключа
не вызывает запрос. Timeout охватывает limiter wait и HTTP; raw evidence и
несколько MBIDs сохраняются без выбора первого. Полный `task verify` прошёл,
независимое ревью adapters закрыто. Application composition общего gate,
provider jobs/cache mapping и automatic matching ещё не подключены; M05 и весь
план не объявляются завершёнными. Dependency licenses включены в notices.

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
   may be implementation safeguards, not added product acceptance. Matching
   performs no source file I/O; the precise source stat contract is technical and
   must align (B10) — do not invent fence relaxation or default backfill/reanalysis.
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

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> «exact transferred score outputs» superseded: matching tests фиксируют
> versioned/documented Go-оценки с ожидаемым отличием, не bit-exact parity (B11).

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

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> требование точного переноса score-normalization из этой формулировки superseded:
> идиоматичный Go-аналог, без Python-таблиц/RapidFuzz целиком, не bit-exact (B11).

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
переопределяет сохранение повторов из A3, B6 supersedes прежнее требование
«встроенный MBID участвует в общей matching-формуле», а B11 supersedes 1:1-перенос
Python/RapidFuzz (точный score-normalization, скомпилированную таблицу B9 и
bit-exact fixtures). Реализация не заявляется.

## B1. Теги latest: сохранение, списки и дубликаты

- Сохраняются все исходные теги; observed tags нельзя подменять или отбрасывать.
- Повторяющиеся теги (например `ARTIST=artist1` и `ARTIST=artist2`) — это список,
  а не конфликт; мультиартисты не сворачиваются к первому имени.
- Дубликаты значений могут дедуплицироваться с сохранением порядка «первое
  вхождение». Это технический порядок, безопасно сохраняющий прежний порядок, а
  не выбранная владельцем новая сортировка; сортировка не вводится. Это
  переопределяет сохранение повторов из
  [A3](#appendix-a-owner-decisions-2026-10-09).
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

## B7. Assignment: legacy combined release+recording factors; release selection отдельно

- Assignment использует точные legacy-факторы combined release + recording,
  duration и MusicBrainz search (дважды) — ту же переносимую формулу, что и
  scoring; новых факторов/весов не вводится.
- Выбор release — отдельный шаг со своим прежним контрактом (qualified >= 0.70 для
  каждого файла, unique max sum, tie → review) и не смешивается с assignment
  ([A2](#appendix-a-owner-decisions-2026-10-09)).

## B8. Source/provider milliseconds: legacy rounding до seconds

- Значения duration из source/provider в миллисекундах переводятся в секунды
  legacy-округлением (Python `round`, ties-to-even) до применения неизменной
  duration-формулы; сама формула не меняется.

## B9. Reference normalization: compiled RapidFuzz 3.14.6 + Unidecode 1.4.0, standalone Go

- Reference normalization явно привязывается к скомпилированному RapidFuzz 3.14.6
  (не Python fallback) и Unidecode 1.4.0.
- Точная семантика реализуется standalone на Go; Python runtime не требуется.

> **Позднейшее уточнение 2026-10-09 (см. [Приложение B](#appendix-b-owner-decisions-late-2026-10-09)):**
> скомпилированная таблица RapidFuzz/Unidecode и standalone exact-семантика
> superseded: идиоматичный Go-аналог на нормальных Go-библиотеках, без Python-
> таблиц и mapping-generator, не bit-exact (B11).

## B10. Acquisition метаданных/тегов — часть анализа; matching не читает исходные файлы

Точная формулировка владельца:

> «Вытаскивание из источника и сохранение метаданных (включая теги) должно быть
> частью АНАЛИЗА файла, а не матчинга. Матчинг происходит уже по известным
> анализам и сравнению их с провайдерами, мы не должны в матчинге вообще исходные
> файлы трогать».

- Однозначно: вытаскивание из источника и сохранение метаданных, включая теги, —
  часть **анализа** файла.
- Matching работает только по уже известным сохранённым analyses и их сравнению с
  провайдерами; matching не читает исходные файлы (no source file I/O).
- Ранее предложенное получение тегов на фазе matching **отклонено**, не одобрено.
- Вопрос binary payload / зависимости (в том числе `go-mp4`) владельцем пока не
  одобрен; не выбирать и не предполагать.
- Исторические факты не переписываются; существующего одобрения, обязывающего
  custom/native/raw collector, нет.

> **Reference note — source fencing:** DB-known source/analysis identity
> проверяется как fence, но точный контракт source stat остаётся техническим и
> должен быть согласован; matching при этом не делает file I/O. Не изобретать
> критерии ослабления fence и не добавлять default backfill/reanalysis. Lifecycle
> обновления метаданных уже известной inventory остаётся нерешённым.

> **M03 misconception:** формулировка «all unknown tags needs owner» неверна —
> владелец уже явно хочет сохранять все теги (B1); это не открытый owner gate.
> Research doc M03 другим writer'ом не правится; уточнение фиксируется здесь, в
> плане.

> **Датированное дополнение 2026-10-09:** отдельный research doc M03 перенесён
> внутрь плана как [Приложение D](#appendix-d-m03-tag-mapping-research);
> standalone-файл удалён. Техническая рекомендация `mtag` из него помечена
> obsolete и superseded решением B14 (TagLib / `go-taglib`). Исторические факты
> исследования сохранены в Приложении D.

## B11. Go-аналог вместо 1:1 переноса Python/RapidFuzz

Сокращённая цитата решения владельца:

> «МЫ БЕРЁМ PYTHON РЕАЛИЗАЦИЮ НЕ 1:1, НЕ ТАЩИМ ВСЁ ПОДРЯД, А НОРМАЛЬНО ПИШЕМ
> АНАЛОГ В GO СПЕЦИФИКЕ И ВСЕ БИБЛИОТЕКИ МЕНЯЕМ НА ВАРИАНТЫ НА GO. НЕ НАДО ТАЩИТЬ
> КАКИЕ-ТО ТАБЛИЦЫ ИЗ КАКОЙ-ТО БИБЛИОТЕКИ НА PYTHON, НЕ НАДО ТАЩИТЬ АЛГОРИТМ
> ЦЕЛИКОМ ИЗ RAPIDFUZZ, возьми ... нормальные Go библиотеки в замену».

- Пишем идиоматичный аналог логики на Go; нормальные Go-зависимости для fuzzy и
  транслитерации используются как замена Python-библиотек.
- **Не** переносим Python-таблицы и не генерируем mapping-generator из Python.
- **Не** переносим алгоритм RapidFuzz целиком.
- Bit-exact parity с Python не является gate; resulting Go text scores versioned и
  документированы, ожидаемое отличие допустимо и не называется bit-exact.
- Product math не меняется: factors, weights, duration, threshold, release
  selection, all-files rule и manual — как прежде (B7/B8, A2).
- Конкретные Go-библиотеки не объявляются одобренными до технической
  рекомендации; владелец разрешает Go-замены.
- B11 supersedes требование точного переноса score-normalization (A3/текст),
  скомпилированную таблицу B9 и «1:1» fixtures/exact transferred score outputs.

## B12. Готовые Go-библиотеки вместо самописных решений

Точная формулировка владельца:

> «Если на что-то есть готовая go библиотека мы берём готовую go библиотеку, мы
> не пишем велосипеды сами!»

- Где для задачи есть готовая Go-библиотека — берём готовую; собственные
  велосипеды (fuzzy/LCS, таблицы, декодирование и т.п.) не пишем.
- **Технический выбор M02 (не индивидуальное одобрение библиотек).** Владелец дал
  широкое разрешение на Go-замены, а не выбирал отдельно метрику или конкретную
  библиотеку; выбор библиотеки/метрики — техническое решение реализации, а не
  per-library owner approval. Для scoring/text-normalization выбраны:
  - `github.com/adrg/strutil` `v0.3.1` — метрика normalized Levenshtein;
  - `github.com/anyascii/go` `v0.3.3` — транслитерация (лицензия ISC);
  - существующий `golang.org/x/text` `v0.42.0` — Unicode NFC и case folding.
- Не заявлять, что каждая библиотека/метрика одобрена владельцем по отдельности;
  это технический выбор в рамках широкого Go-разрешения.
- Product math не меняется: factors/weights, duration, threshold, tie-правило и
  all-files/all-group правило — как прежде (B7/B8, A2); bit-exact parity с Python
  по-прежнему не gate (B11).
- **Сырые теги — только анализ.** Acquisition тегов/метаданных остаётся частью
  анализа (B10); matching не делает source I/O и работает по сохранённым
  analyses. Кандидат `github.com/tommyo123/mtag` `v1.0.2` технически исследован
  (MIT, Go, без обязательных env vars и без обязательного CLI), но полный
  собственный raw-парсер как default не выбирается. Фактическая интеграция в
  анализ и выбор механизма — решения следующего этапа, ещё не одобренные;
  четвёртый механизм не утверждён.

## B13. Независимый metadata step и разработка с нуля

**Позднейшие решения владельца от 2026-10-09:**

- Извлечение и сохранение тегов через готовую Go-библиотеку — четвёртый
  независимый шаг существующего source-analysis engine, рядом с SHA, ffprobe
  и fingerprint. Используется тот же prepared input, не второй pipeline.
- При ошибке metadata сохраняются успехи соседних шагов; ошибка видна как
  ошибка анализа. Retry повторяет только ошибочный шаг.
- Приложение не опубликовано, ведётся разработка с нуля. Владелец не требует
  обратной совместимости, миграции старых установок или backfill существующих
  анализов. Не добавлять такие сценарии как продуктовые обязательства и не
  вводить специальные действия обновления исторических captures.
- Физическая схема и тесты строятся под актуальную модель. SQL migrations
  остаются способом создания схемы и имеют проверяемый down path; это не
  требование сохранять совместимость со старыми версиями dev-приложения.
- Упоминания populated upgrade, сохранения historical values и обязательного
  отображения старых analysis intent в теле плана не задают новый compatibility
  scope. Проверяются актуальные schema/constraints/transactions и выбранный
  migration/down path, а не миграция опубликованной старой установки.
- Это закрывает вопрос механизма из B12 и вопрос обновления исторической
  inventory из B10. Matching по-прежнему не делает source file I/O.

## B14. Metadata reader: TagLib (go-taglib), ограниченный набор тегов и MKA projection

**Позднейшие решения владельца от 2026-10-09 (уточняют механизм из B13).**

- Извлечение тегов выполняется через **TagLib** (`go-taglib`). Владелец принял,
  что библиотека возвращает **ограниченный набор тегов** (accepted limited
  returned tags), а не полный raw dump всех контейнеров.
- `Properties` — **опциональны**: их использование не обязательно.
- **ffprobe остаётся техническим probe** и не заменяется. TagLib — metadata-шаг
  рядом с существующими шагами анализа (SHA/ffprobe/fingerprint), а не замена
  ffprobe.
- **MKA:** файл сохраняется (keep); projection тегов — **UID 0 соответствует
  всем** (matches all). Используется **sole audio** трек; применяется union
  global tags + soleAudio track tags; теги нескольких аудио-треков **не
  смешиваются** (no multi-audio mix).
- Это **supersedes** техническую рекомендацию `github.com/tommyo123/mtag`
  `v1.0.2` из B12 и из [Приложения D](#appendix-d-m03-tag-mapping-research):
  mtag-рекомендация помечена obsolete. Нормализованные возвращаемые теги —
  от TagLib (`go-taglib`).
- Историческая фиксация `mtag` остаётся в
  [Приложении D](#appendix-d-m03-tag-mapping-research) как исследованный, но не
  выбранный кандидат.
- B10/B13 сохраняются: acquisition тегов/метаданных — часть анализа; matching
  не делает source file I/O; при ошибке metadata успехи соседних шагов
  сохраняются, retry повторяет только ошибочный шаг.

## B15. AcoustID application key: write-only настройка, отсутствие ключа = нет lookup, без отдельного toggle

**Дата фиксации: 2026-10-10. Статус: явное продуктовое решение владельца.**
Позднейшее уточнение контракта AcoustID; оно не переписывает исторические факты
и текст Приложений A/B от 2026-10-09. При конфликте приоритет имеют решения
ниже. Реализация этим приложением не заявляется.

Владелец подтвердил предложенный контракт дословно: «Да, такой контракт».

- Application API key AcoustID хранится как runtime-настройка в PostgreSQL
  (DB-backed), а не как env var.
- Настройка **write-only**: сохранённый ключ никогда не возвращается клиенту
  через API. Ключ не попадает в логи и не помещается в diagnostic URLs; никакой
  диагностический вывод не раскрывает значение ключа.
- Оператор может **заменить или удалить** ключ. Отдельного независимого
  AcoustID enable/disable toggle не вводится: включение AcoustID определяется
  наличием сохранённого ключа, а не отдельным флагом.
- Если ключ **отсутствует (ABSENT)**, AcoustID lookup не выполняется.
- MusicBrainz matching остаётся работоспособным без ключа: AcoustID — optional
  factor. При отсутствии ключа его factor отсутствует (не «совпал», не штраф);
  отсутствие AcoustID не блокирует matching и не меняет прочие контракты.
- Это уточняет строку реестра «AcoustID» и §«Поиск candidates и evidence»;
  optionality AcoustID, «отсутствие = не совпадение», запрет штрафа, source
  read-only и publication вне scope сохраняются.

> **Статус реализации:** это продуктовый контракт, а не подтверждение
> выполнения provider foundation/M05. Реализация MusicBrainz/AcoustID provider и
> settings этим документом не заявляется завершённой или проверенной; она
> остаётся за review gate соответствующих этапов. Исторические датированные
> записи 2026-10-09 не переписываются.

---

# Приложение C. План 11, M01 — provider и River research appendix

<a id="appendix-c-m01-provider-and-river-research"></a>

**Дата проверки внешних источников:** 2026-10-09.
**Назначение:** документальная фиксация baseline и upstream facts для M01 плана 11.
**Статус:** исследование; это не продуктовый контракт и не подтверждение выполнения следующих этапов.

**Позднейшее решение владельца, 2026-10-09:** Python служит reference логики,
не целью переноса 1:1. RapidFuzz и Unidecode, их внутренние алгоритмы и таблицы
не переносятся в приложение. M02 использует готовые Go-библиотеки; см.
[B11–B12 плана](#appendix-b-owner-decisions-late-2026-10-09).
Исторические сведения об upstream requirements и лицензиях ниже остаются
результатом исследования, а не списком зависимостей MeloTrove.

## Baseline репозитория

- `git rev-parse HEAD`: `8c205db526d23e05279e17e185d11a2cb7a6813c`.
- Уточнение baseline при дополнении 2026-10-09: commit `671759030f32893161bc87c4ab0e7ca36be9c1ec` содержит только изменение документации плана 11 и не меняет исследованную кодовую ревизию `8c205db526d23e05279e17e185d11a2cb7a6813c`. Последующий HEAD `c26bf689c37179a6dd90be8f69ab9bca40936a03` также меняет только документацию плана 11 (coverage wording и ссылку M01); исследование кода по-прежнему привязано к 8c205db. Внешняя PostgreSQL документация и upstream clone проверены отдельно.
- До этой работы `git status --short` показывал уже изменённый `docs/plans/todo/11-incoming-grouping-and-automatic-matching.md` и неотслеживаемый `test_stand/`. Эти предварительные изменения не принадлежат M01 appendix; не трактовать их как результат M01 и не перезаписывать.
- Backend объявляет Go `1.27` (`backend/go.mod`); локально проверенная версия — `go1.27.1 darwin/arm64`. На дату доступа текущая документация `pkg.go.dev/net/http` опубликована для Go `1.27.2` (8 октября 2026); для версионной HTTP-семантики ниже используется именно эта версия, а не предположение о версии локального toolchain.
- `backend/go.mod` и `backend/go.sum` фиксируют River `v0.48.0` (включая `riverdatabasesql` и `rivertype`). Официальные River pages ниже — текущие docs на дату доступа, не архивная документация, привязанная к `v0.48.0`.
- В коде на baseline MusicBrainz ограничен connectivity check в `backend/internal/integrations/musicbrainz/client.go`: официальный WS/2 endpoint, `http.Client.Timeout = 10s`, контекст запроса, JSON и ограничение тела 1 MiB. Настраивается `User-Agent`; production release/recording search, cache, общий rate limiter и AcoustID adapter в этой границе не обнаружены. Конфигурация public/self-hosted находится в settings/service; текущий base URL — существующая настройка, не новая abstraction для matching.
- Существующие операции сохраняют состояние и ставят River jobs через `InsertTx` в Bun/PostgreSQL transaction, например `backend/internal/persistence/setup_manager.go`, `source_scan_enqueue.go`, `source_scan_retry.go`. В persistence применяются транзакционные row locks (`FOR UPDATE`) и точечные PostgreSQL advisory locks; пример для анализа — `source_analysis_steps.go:122-145,631-665`, где порядок lock-ов начинается с `source_root`, затем `source_location`, `source_analysis_work`, `operation` и step. Это наблюдаемые примеры текущего кода, а не утверждённый порядок lock-ов для будущего matching.
- На baseline верхняя версия встроенной SQL-миграции — `20261027000000_source_analysis_artifact_requested_steps.tx.{up,down}.sql`. DBML в `docs/design/music_ingest_redesign.dbml` концептуален: он не доказывает наличия matching schema.
- Текущие boundaries подтверждаются `docs/design/repository-architecture.md` и `docs/design/decisions.md`: integrations владеет внешним HTTP/parsing; service оркестрирует; только persistence обращается к Bun/PostgreSQL; `InsertTx` позволяет одной транзакцией записать application state и поставить job. Это исследование не меняет их.

## Официальные provider facts

Все ссылки ниже проверены 2026-10-09. Ревизии MusicBrainz взяты из footer самих страниц, а не выведены из даты доступа.

### MusicBrainz WS/2

Источники: [MusicBrainz API](https://musicbrainz.org/doc/MusicBrainz_API), ревизия wiki `79405`; [Rate Limiting](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting), ревизия `78895`; [Search](https://musicbrainz.org/doc/MusicBrainz_API/Search). Основной JSON endpoint документирован как `https://musicbrainz.org/ws/2/`; JSON выбирается через `fmt=json` или `Accept: application/json`.

Проверенные facts:

- Для публичного сервиса клиентское приложение не должно превышать **один запрос в секунду**; rate limiting отдельно рассматривает User-Agent, IP и общую нагрузку. Превышение/перегрузка может привести к `503 Service Unavailable`. IP rule применяет блокировку к запросам с IP, пока частота не опустится до 1/s или ниже; это не обещание, что отдельные лишние запросы просто будут обслужены с меньшей частотой.
- Каждому запросу необходим содержательный `User-Agent` с названием приложения и достаточной контактной информацией; версия рекомендована. Документированные примеры имеют форму `Application/version (contact-url-or-email)`. Нынешний connectivity client уже задаёт `MeloTrove/0.1.0 (https://github.com/ruckus/MusicEnreachment)`.
- Lookup по MBID может включать связанные сущности, но число возвращаемых linked entities ограничено 25; за остальными данными требуется browse. Browse поддерживает `offset`, а `limit` — максимум 100.
- Browse `/release` дополнительно ограничивает совокупный результат страницы 500 треками: страница может содержать меньше запрошенного количества релизов, но релиз не делится. Для paging `offset` следует увеличивать на фактическое число полученных релизов, а не на заданный `limit`.
- Search принимает `limit` от 1 до 100, по умолчанию 25, и `offset` для paging. Search не становится lookup/browse; ответ содержит результаты Lucene query.
- Публичный MusicBrainz WS — бесплатен для некоммерческого использования согласно API FAQ. Эта информация не разрешает коммерческое использование.

**Граница переноса:** публичный лимит относится к публичному MusicBrainz; не распространять его автоматически на self-hosted инстанс. Owner decisions по отдельному self-hosted toggle/default/delay находятся в [Приложении A плана](#appendix-a-owner-decisions-2026-10-09), а не выводятся из этой policy. Официальная API documentation не задаёт продукту cache freshness, batch size, внутренний RPS default либо стратегию повторов.

### AcoustID lookup

Источник: [AcoustID Web Service](https://acoustid.org/webservice), проверен 2026-10-09. Lookup endpoint: `https://api.acoustid.org/v2/lookup`.

Проверенные facts:

- Для fingerprint lookup необходимы application API key (`client`), длительность всего аудиофайла в секундах (`duration`) и fingerprint (`fingerprint`). Можно запросить recording IDs/metadata через `meta`; найденные MusicBrainz recordings — связанное evidence.
- Сервис разрешает GET и POST, причём документация предпочитает сжатый POST для длинных fingerprints. Параметры lookup описаны как параметры запроса. Для данного приложения передача application key и fingerprint в POST body согласуется с требованием `docs/design/decisions.md` не помещать credentials в URL; это обоснование для реализации, а не отдельное правило AcoustID.
- Указан предел **не более 3 запросов в секунду**. Указано **non-commercial use only**; для коммерческого применения сайт направляет к отдельному коммерческому сервису.
- Выполнение lookup — необязательное evidence: owner contract плана 11 устанавливает optionality и обработку отсутствующего AcoustID factor. API docs не устанавливают для приложения default включения, cache freshness или расписание запросов.

В scope M01 не входят fingerprint submission, пользовательский API key для submission и изменение данных AcoustID; документируемый для matching use case — lookup evidence для recording.

### Go `net/http`

Источник: [pkg.go.dev/net/http для Go 1.27.2](https://pkg.go.dev/net/http@go1.27.2), текущая опубликованная версия на 2026-10-09.

- `http.NewRequestWithContext` связывает контекст с lifecycle request; отмена/timeout контекста применяются, пока отправляется запрос, получен ответ и читается response body.
- `http.Client.Timeout` охватывает connect, redirects и чтение response body; timer продолжает действовать после возврата `Do` и может прервать чтение body. В репозитории connectivity client задаёт 10s timeout, дополнительно принимая caller context.
- `Client.Do` сам по себе не трактует HTTP status вне 2xx как Go error; вызывающая сторона должна прочитать/закрыть body и интерпретировать status. Это релевантно будущей обработке `503`/rate limit, но не утверждает конкретную политику retry.
- `net/http.Transport` может в некоторых случаях автоматически повторить идемпотентный запрос при ранее использованном соединении; условия зависят от replayable body/идемпотентности и ошибки. Поэтому официальная документация не даёт основания утверждать, что встроенных повторов «нет вообще». При внедрении rate limiter нужно учитывать возможную повторную HTTP отправку и не объявлять число вызовов `Do` равным точному числу wire requests без проверки конкретного поведения.

Контекст/timeout — механизм ограничения и отмены HTTP работы, не политика freshness, retries, backoff или частоты провайдера.

## River facts и наблюдаемая практика репозитория

Официальные источники, проверенные 2026-10-09: [Transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing), [Job retries](https://riverqueue.com/docs/job-retries), [Writing reliable workers](https://riverqueue.com/docs/reliable-workers), [Unique jobs](https://riverqueue.com/docs/unique-jobs).

- Транзакционный enqueue связывает вставку job с прикладными изменениями в одной транзакции: job становится доступной после commit вместе с состоянием, от которого зависит. Репозиторий использует `InsertTx` для этого паттерна.
- Ошибки/сбои могут приводить к retry; workers должны наследовать и уважать `context` и быть безопасны к повторному исполнению. Документированный default River retries — максимум 25 попыток с экспоненциальной задержкой/jitter; не следует принимать default этой библиотеки за утверждённую retry-политику matching.
- Unique jobs ограничивают повторную **вставку** по заданным атрибутам/состояниям. Они не обеспечивают exactly-once execution: River описывает выполнение как at-least-once, поэтому приложение должно идемпотентно применять side effects и иметь собственные DB fences/constraints.
- `InsertTx`/unique job semantics не выбирают автоматически idempotency key, допустимость повторного применения устаревшего результата, recovery lifecycle или модель matching cache. Это остаётся design/implementation scope соответствующих этапов.

## Upstream scoring и fixture provenance

Проверена read-only shallow clone, указанный в плане: `/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/upstream_repo`, `HEAD=17448df1ee7ab5c9ad4769b4e09a474b27547728`. Clone не содержит найденного файла `LICENSE`/`COPYING`; отсутствие файла в shallow clone не является юридическим выводом об upstream лицензии.

- Ниже перечислены provider fixture files, читаемые matching tests через `tests/support/providers.py` (`FIXTURES_DIRECTORY / provider / <FixtureCase>.json`), и файлы inline/scoring regression cases. Для всех перечисленных путей source commit в исследованном clone — `17448df1ee7ab5c9ad4769b4e09a474b27547728`; в clone на этом commit отсутствует найденный LICENSE/COPYING. Provenance помечен по наблюдаемой форме/комментариям тестов; где первичный источник не записан, он так и отмечен как неизвестный.

| Путь в upstream | Наблюдаемый origin | License/provenance disposition |
| --- | --- | --- |
| `tests/fixtures/musicbrainz/ambiguous.json` | Synthetic fixture-control JSON (`outcome`, `candidate_count`), не MusicBrainz wire response | Exact upstream file owner-approved для переноса по A7; отдельная лицензия/атрибуция файла в clone не указана. |
| `tests/fixtures/musicbrainz/disabled.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/malformed.json` | Synthetic malformed fixture-control JSON (незакрытая JSON value) | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/no_match.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/rate_limited.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/success.json` | Synthetic fixture DTO (`Fixture Release`/`Fixture Artist`, тестовые MBID); не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/timeout.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/musicbrainz/unavailable.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/ambiguous.json` | Synthetic fixture-control JSON (`outcome`, `candidate_count`), не AcoustID wire response | Exact upstream file owner-approved для переноса по A7; отдельная лицензия/атрибуция файла в clone не указана. |
| `tests/fixtures/acoustid/disabled.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/malformed.json` | Synthetic malformed fixture-control JSON (незакрытая JSON value) | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/no_match.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/noize-pesnya-dlya-radio.json` | Test comment называет содержимое «verbatim current AcoustID response captured for the Noize MC source»; response hash закреплён regression test. Но запрос использует placeholder `'noize-fingerprint'`, не сохранённый реальный fingerprint; provenance реального lookup/input не установлена. | A7 даёт owner approval для переноса upstream fixture. Точная лицензия/provider attribution и происхождение захваченного response отдельно в clone не записаны — считать эти детали неизвестными, сохранять известное attribution и проверять применимые требования при переносе. |
| `tests/fixtures/acoustid/rate_limited.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/success.json` | Synthetic fixture DTO с `Fixture` recording MBID и тестовым score; не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/timeout.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/fixtures/acoustid/unavailable.json` | Synthetic fixture-control JSON, не provider response | То же: A7 owner approval; отдельная лицензия не указана. |
| `tests/matching/test_matching.py` | Inline constructed score inputs/candidates; содержащиеся реальные на вид названия/имена не снабжены отдельной source provenance в test file | Владелец одобрил использование upstream fixture material по A7; отдельная лицензия test file не обнаружена. Для каждого фактически переносимого real-world literal точная исходная provenance неизвестна. |
| `tests/matching/test_matching_providers.py` | Inline synthetic transport JSON/test objects, в основном явно названные `Fixture`; test file также содержит реалистичные названия/IDs без per-literal provenance | A7 owner approval для upstream test material; отдельная лицензия test file не обнаружена. Не объявлять real-looking literals синтетическими provider captures; их внешняя provenance не зафиксирована. |
| `tests/matching/test_noize_matching_regression.py` | Regression consumer/inline test, который читает указанный Noize JSON и передаёт `'noize-fingerprint'`; реального fingerprint input в test нет | A7 owner approval для файла; provenance/лицензия response ограничены сведениями из строки таблицы выше и не становятся установленными только из test name. |

`test_matching.py` — scoring-focused family (406 строк); `test_matching_providers.py` (1319 строк) покрывает также inline provider adapter/evidence cases; `test_noize_matching_regression.py` — 92 строки. Noize JSON не является fingerprint corpus и не доказывает live-network lookup. SHA-256 содержимого `tests/fixtures/acoustid/noize-pesnya-dlya-radio.json`, зафиксированный test assertion: `048562094731e7993eeb78a4cae77adb552c8a54f01b2af08d43e2902efee3a0`.

- В `pyproject.toml` upstream указаны `rapidfuzz>=3.14.6` и `Unidecode==1.4.0`. Зафиксированная owner-approved лицензируемость upstream fixtures/семантики и обязательство реализовать логику самостоятельно на Go отражены в A7 плана 11. Это не делает Python-код runtime dependency и не меняет выбор разрешённых в проекте зависимостей.
- Справочные license sources для этих upstream requirements: [RapidFuzz license](https://github.com/rapidfuzz/RapidFuzz/blob/main/LICENSE) — MIT; [PyPI Unidecode 1.4.0](https://pypi.org/project/Unidecode/1.4.0/) — GNU GPL v2 or later (GPLv2+). Допустимость, подтверждённая владельцем, не отменяет выполнения применимых требований лицензии/атрибуции, если fixture или производный материал действительно переносится; это требует compliance-проверки при таком переносе.
- Для переноса fixture corpus точные источники каждого значения/записи и происхождение Noize fixture следует сохранять. Личная собственность upstream и допустимость лицензий подтверждены владельцем; это снимает owner approval gate, но не отменяет соблюдение применимых уведомлений/атрибуции при фактическом переносе файлов.
- Инвентаризация выше ограничена тремя matching tests и shallow clone. Она не является полным SBOM/лицензионным аудитом всего upstream-репозитория или всех его зависимостей.

## PostgreSQL locking semantics и локальная практика

Источники проверены 2026-10-09 в версионной документации [PostgreSQL 18, Explicit Locking](https://www.postgresql.org/docs/18/explicit-locking.html) и [PostgreSQL 18, Advisory Lock Functions](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADVISORY-LOCKS). На дату проверки документация указывает PostgreSQL 18 как current supported version; это версия документации, не заявление о конкретной runtime-версии установленной БД.

**Официальные факты:**

- Row-level locks удерживаются до завершения транзакции (либо соответствующего savepoint rollback). `SELECT ... FOR UPDATE` блокирует конкурирующие записи и несовместимые row-lock requests на затронутых строках; обычный SELECT без row lock не блокируется row lock-ом.
- Взаимные ожидания блокировок могут образовать deadlock; PostgreSQL обнаруживает deadlock и abort-ит одну транзакцию. Docs рекомендуют брать locks на несколько объектов в согласованном порядке. Ожидание конфликтующей блокировки без deadlock может продолжаться без ограничения времени.
- Advisory locks — application-defined; PostgreSQL не навязывает их протокол и не гарантирует, что все конкурирующие участники берут тот же lock. Session-level lock держится до явного unlock либо конца session и не откатывается вместе с transaction rollback. Transaction-level (`*_xact_lock`) автоматически освобождается в конце transaction и не требует unlock.
- PostgreSQL предоставляет key формы двух `int4` или одного `int8`; shared/exclusive варианты существуют как для session-, так и для transaction-level locks. Это механизм сериализации только между участниками, соблюдающими одинаковые lock keys/protocol.

**Наблюдаемое соответствие кода (не контракт для будущего matching):**

- `backend/internal/persistence/source_analysis_coordination.go`: `advisoryXactLock` формирует вызов `pg_advisory_xact_lock` или `_shared` из пары `int32`; namespace `0x4d545256` (`MTRV`) и domain keys заданы явно. `lockPackageKinds` сортирует package kinds перед взятием нескольких advisory locks; комментарий явно запрещает `hashtext` для этих ключей.
- `backend/internal/persistence/source_coordination.go`: package activation использует те же sorted advisory keys; `lockToolsRoots` сортирует roots, а SQL запрашивает их `ORDER BY id` до row-lock. Это две наблюдаемые меры порядка в соответствующих call paths, а не глобальная гарантия для всех операций.
- `backend/internal/persistence/output_admission_coordination.go`: output admission gate берёт shared transaction-level advisory lock, reset gate — exclusive transaction-level lock. Отдельный `WithExclusiveOutputAdmissionSession` закрепляет `*sql.Conn`, берёт `pg_advisory_lock` session-level и явно делает `pg_advisory_unlock` на том же соединении; это связано с callback/journal/filesystem flow и отличается lifecycle-ом от обычного xact helper.
- `backend/internal/persistence/source_analysis_steps.go` также демонстрирует row-lock порядок `source_root → source_location → source_analysis_work → operation → source_analysis_step`. Никакой из этих примеров не утверждает, что будущая matching-транзакция должна копировать этот порядок без собственного design/review.

PostgreSQL facts не выбирают продуктовую модель или lock protocol matching; точный стабильный порядок и transaction scope для будущих group apply/cache operations остаются техническим design/review вопросом.

## Facts и решения, которые остаются открытыми

Подтверждённые выше лимиты, HTTP semantics и River guarantees — **внешние факты**. Одобренные продуктовые решения остаются в owner appendix плана. Это исследование не принимает дополнительных продуктовых решений и не выбирает технический механизм.

Перед соответствующими increments ещё требуется отдельно определить и проверить: provider cache freshness/invalidation; конкретный compliant limiter/semaphore и поведение при `503`; self-hosted endpoint compatibility assumptions; правила AcoustID key transport/storage в соответствии с проектным secret policy; применение query/browse/lookup и dedup/paging к конкретным сущностям; соответствие River integration/docs версии `v0.48.0`; idempotency, lock order, fences и recovery для matching transactions. Не переносить текущие timeout, file concurrency=4 или чужие library defaults как новые RPS, cache TTL, retry или persistence decisions.

## Проверенные локальные материалы

- `docs/design/decisions.md`, `docs/design/repository-architecture.md`, `docs/design/data-model.md`, `docs/design/music_ingest_redesign.dbml`.
- `backend/internal/integrations/musicbrainz/client.go`; `backend/internal/settings/settings.go`; `backend/internal/service/setup.go`.
- `backend/internal/persistence/setup_manager.go`, `source_scan_enqueue.go`, `source_scan_retry.go`, `source_analysis_steps.go`; `backend/internal/jobs/`.
- `backend/go.mod`, `backend/go.sum`, `backend/internal/migrations/`; `frontend/src/routes/AppShell.tsx`, `frontend/src/features/sources/`.

Файлы и directory boundaries перечислены как места baseline inspection, а не свидетельство наличия в них matching implementation. Codegraph/исходный код и миграции проверялись на code baseline `8c205db526d23e05279e17e185d11a2cb7a6813c`; HEAD на момент этого уточнения `c26bf689c37179a6dd90be8f69ab9bca40936a03` содержит только более поздние documentation-only изменения. Локальные тесты не запускались, поскольку это документационное уточнение M01.

---

# Приложение D. План 11, M03 — исследование сопоставления тегов (tag mapping research appendix)

<a id="appendix-d-m03-tag-mapping-research"></a>

**Дата:** 2026-10-09.
**Назначение:** документальная фиксация фактов о тегах и их сопоставлении для M03
плана 11.
**Статус:** исследование. Это не продуктовый контракт, не подтверждение
выполнения этапов и не приёмка. Документ не принимает продуктовых решений, не
меняет план, не утверждает дизайн кода и не заявляет пройденных проверок.

> **Датированное дополнение 2026-10-09:** этот раздел перенесён внутрь плана из
> standalone research doc M03; файл удалён. Техническая рекомендация `mtag` ниже
> помечена obsolete и superseded решением [B14](#appendix-b-owner-decisions-late-2026-10-09)
> (TagLib / `go-taglib`). Исторические факты сохранены без правки.

## Границы и честность изложения

- Это research-only appendix. Продуктовые решения о группировке/matching остаются
  в [Приложении A](#appendix-a-owner-decisions-2026-10-09)
  и [Приложении B](#appendix-b-owner-decisions-late-2026-10-09)
  плана 11; здесь новых решений не вводится.
- **Прямой дизайн «сырого» декодера тегов не утверждён.** Любая схема
  самостоятельного разбора контейнеров/тегов — вопрос для архитектурного
  ревью, а не согласованное решение. Этот документ такой дизайн не фиксирует.
- **Нет заявки на test acceptance.** Наблюдения о поведении декодеров ниже —
  это эмпирические факты из read-only исследования librarian (включая его
  fixtures), а не результат прогона тестов этого репозитория. Они не являются
  приёмочным доказательством и не заменяют будущие regression fixtures.
- Таблица ниже — **практически предлагаемое сопоставление (proposed)**, не
  утверждённое окончательно. Официальные имена тегов зависят от теггера
  (custom labels) и от конкретной программы; канонические имена MusicBrainz
  Picard не подтверждены официальным источником в этом исследовании (см. ниже).
- При конфликте с решениями владельца или `docs/design/` приоритет имеют они.

## Официальные источники

### Vorbis comment — проверено

Источник: [Ogg Vorbis I format specification: comment field and header
specification](https://xiph.org/vorbis/doc/v-comment.html), проверено
2026-10-09. Подтверждённые факты из спецификации:

- Поле комментария имеет вид `NAME=value`; имя поля не зависит от регистра
  (ASCII `A–Z` эквивалентны `a–z`), значение — UTF-8 до конца поля.
- **Имена полей не обязаны быть уникальными.** Спецификация прямо приводит
  пример нескольких `ARTIST=` (Dizzy Gillespie / Sonny Rollins / Sonny Stitt) как
  допустимый и поощряемый: повторяющиеся теги — это список, а не ошибка.
- Стандартный минимальный набор имён: `TITLE`, `VERSION`, `ALBUM`, `TRACKNUMBER`,
  `ARTIST`, `PERFORMER`, `COPYRIGHT`, `LICENSE`, `ORGANIZATION`, `DESCRIPTION`,
  `GENRE`, `DATE`, `LOCATION`, `CONTACT`, `ISRC`.
- Спецификация ссылается на реализацию `vorbis/lib/info.c`:
  `_vorbis_pack_comment()` / `_vorbis_unpack_comment()`.
- Спецификация **не** определяет `ALBUMARTIST`, `CATALOGNUMBER`, `RELEASECOUNTRY`
  или `MUSICBRAINZ_*`; это теггер-конвенции (в частности Picard), а не часть
  Vorbis-спецификации. Это различие важно: имена MusicBrainz-тегов ниже —
  предлагаемое сопоставление, а не цитата из Vorbis-спеки.

### FFmpeg / ffprobe — read-only исследование исходников librarian

- Факты о поведении декодеров ffmpeg ниже получены librarian из read-only
  исследования исходников FFmpeg на **текущем master, без закреплённой ревизии
  (unpinned)**. Это **не общая гарантия для всех сборок/версий**: поведение
  может меняться между релизами, а конкретная версия управляемого ffprobe
  проекта не закреплена (каталог выбирает релизы на рантайме, см.
  `backend/internal/integrations/tools/catalog.go`).
- Поэтому любые утверждения о декодерах следует читать как «наблюдено на
  исследованной ревизии master», а не как «так устроено во всех сборках».
- Официальная документация ffprobe (форма вывода `-show_format`/`-show_streams`)
  остаётся внешним ориентиром; точная ревизия/коммит для закрепления ещё не
  выбрана и здесь не фиксируется.

> **Позднейшее уточнение 2026-10-09 (см. [B14](#appendix-b-owner-decisions-late-2026-10-09)):**
> ffprobe остаётся техническим probe; metadata-теги извлекаются отдельным
> metadata-шагом через TagLib (`go-taglib`), ffprobe не заменяется.

### MusicBrainz Picard tag mapping — недоступно, НЕ цитируется как проверенное

- URL из задания `https://picard-docs.musicbrainz.org/en/appendices/tag_mapping.html`
  вернул HTTP 404 на 2026-10-09. Официальная страница Picard tag mapping по
  этому адресу недоступна.
- Поэтому канонические имена Picard **не приводятся как проверенный
  официальный источник**. Значения MusicBrainz-тегов в таблице ниже — это
  широко распространённая практика теггеров и предлагаемое сопоставление, а не
  цитата из подтверждённой документации Picard.
- Повторный fetch не выполнялся; факт 404 зафиксирован как ограничение.

## Текущее состояние кода (baseline, не контракт)

- Управляемый probe запускается командой
  `-v error -protocol_whitelist fd -fd <desc> -show_format -show_streams -of json fd:`
  (`backend/internal/integrations/tools/ffprobe_file.go`, `technicalFileArguments`).
  Это один probe, собирающий контейнер и все потоки в один JSON.
- `backend/internal/service/source_technical_analysis.go` строит
  `Tags map[string][]string`: ключи приводятся к верхнему регистру, значения —
  в порядке первого появления, одинаковые строки в рамках одного ключа
  дедуплицируются (`slices.Contains`), строки **не делятся** по `;` или `/`.
  Теги берутся сначала из `format.tags`, затем из `tags` аудиопотоков в порядке
  индекса.
- Утверждённые 13 расширений (`docs/design/deployment.md`): `.flac`, `.wav`,
  `.aif`, `.aiff`, `.ape`, `.wv`, `.mp3`, `.m4a`, `.aac`, `.ogg`, `.opus`,
  `.wma`, `.mka` (без учёта регистра).
- **Ограничение:** команда `-show_format -show_streams -of json` не даёт права
  утверждать, что сохранены все «сырые» теги контейнера. Часть тегов не
  попадает в вывод ffprobe (пример ниже — `UFID`), часть теряет исходную
  множественность (см. ниже). Текущее «сырое» хранение неполно и **не
  реализовано** как полный raw-retention. Владелец уже потребовал сохранять все
  исходные теги, в том числе не участвующие в matching; способ реализации ещё
  предстоит выбрать.

## Наблюдаемое поведение декодеров (эмпирические факты librarian)

Исследование read-only, текущий master FFmpeg, без закреплённой ревизии. Не
общая гарантия для всех сборок.

- **Vorbis (FLAC / OGG / Opus):** повторяющиеся теги в контейнере ffprobe
  **склеивает в одну строку через `;`**. То есть список `ARTIST=A`, `ARTIST=B`
  наблюдается как одно значение `"A;B"`.
- **MP4 / M4A:** повторяющиеся теги также **склеиваются через `;`** в одно
  значение.
- **ID3 (MP3 и др.):** при повторе **побеждает первое** значение (first wins);
  остальные теряются.
- **WAV INFO / APE / ASF (WMA) / Matroska (MKA):** при повторе **побеждает
  последнее** значение (last wins); более ранние перезаписываются.
- **`UFID` (MusicBrainz recording) отсутствует в выводе ffprobe.** Это значит,
  что MusicBrainz recording/track ID, записанный в ID3 `UFID` с owner
  `http://musicbrainz.org`, через текущий probe **не читается**; доступны лишь
  те MusicBrainz-значения, что лежат в обычных текстовых тегах (например TXXX).
- Общий вывод: демультиплексор может потерять множественность ещё до JSON writer;
  текущий объект `tags` не восстанавливает её. Ни одна из стратегий (join `;`,
  first, last) не даёт восстановить полный исходный список повторов.

## Следствия (факты, не решения)

- **Нельзя делить литеральное значение по `;`.** После склейки `"A;B"`
  невозможно отличить реальный разделитель от `;` внутри одного значения.
  Текущий код это соблюдает и не делает split.
- **Список тегов с дедупликацией и стабильным порядком «первое вхождение»**
  работает уже после того, как ffprobe потерял исходную множественность; он
  сохраняет порядок того, что дошло, но не восстанавливает утраченное.
- **Сохранение всех исходных тегов уже требуется владельцем.** Неизвестные имена
  не дают права отбрасывать теги. Хранение и mapping реализуются в анализе;
  matching работает только с сохранёнными результатами, без source file I/O.
- **Country** сравнивается уже утверждённым fuzzy scoring (Приложение B, B2);
  конфликт только по country **не добавляет отдельный veto/review**. Здесь это
  только фиксируется как ограничение сопоставления, без новых правил.

## Предлагаемое сопоставление канонических полей (proposed, не утверждено)

Канонические поля: `artist`, `albumartist`, `album`, `title`, `date`, `catalog`,
`country`, MB release, MB record (track), MB release-track.

| Канон | Vorbis (FLAC/OGG/Opus) | ID3v2 (MP3/AIFF/WAV/AAC) | MP4/M4A | ASF/WMA | Matroska/MKA |
| --- | --- | --- | --- | --- | --- |
| artist | `ARTIST` | `TPE1` | `©ART` | `Author` | `ARTIST` (проверить, не изобретать) |
| albumartist | `ALBUMARTIST` | `TPE2` | `aART` | `WM/AlbumArtist` | `ALBUMARTIST` (проверить) |
| album | `ALBUM` | `TALB` | `©alb` | `WM/AlbumTitle` | `ALBUM` (проверить) |
| title | `TITLE` | `TIT2` | `©nam` | `Title` | `TITLE` (проверить) |
| date | `DATE` | `TDRC` (иначе `TYER`+`TDAT`) | `©day` | `WM/Year` | `DATE` (проверить) |
| catalog | `CATALOGNUMBER` | `TXXX:CATALOGNUMBER` (custom) | `----:com.apple.iTunes:CATALOGNUMBER` | `WM/CatalogNumber` (проверить) | `CATALOGNUMBER` (проверить) |
| country | `RELEASECOUNTRY` | `TXXX:MusicBrainz Album Release Country` (custom) | `----:com.apple.iTunes:MusicBrainz Album Release Country` | `MusicBrainz/Release Country` (proposed) | `RELEASECOUNTRY` (проверить) |
| MB release | `MUSICBRAINZ_ALBUMID` | `TXXX:MusicBrainz Album Id` (custom) | `----:com.apple.iTunes:MusicBrainz Album Id` | `MusicBrainz/Album Id` (proposed) | `MUSICBRAINZ_ALBUMID` (проверить) |
| MB record | `MUSICBRAINZ_TRACKID` | `UFID` (owner `http://musicbrainz.org`) — **ffprobe не читает** | `----:com.apple.iTunes:MusicBrainz Track Id` | `MusicBrainz/Track Id` (proposed) | `MUSICBRAINZ_TRACKID` (проверить) |
| MB release-track | `MUSICBRAINZ_RELEASETRACKID` | `TXXX:MusicBrainz Release Track Id` (custom) | `----:com.apple.iTunes:MusicBrainz Release Track Id` | `MusicBrainz/Release Track Id` (proposed) | `MUSICBRAINZ_RELEASETRACKID` (проверить) |

Пояснения к таблице:

- Колонки Vorbis/ID3/MP4/ASF перечисляют ключи, которые принято использовать в
  теггерах; это **предлагаемое** сопоставление, а не утверждённые официальные
  имена. ID3-варианты `TXXX`/`UFID` и MP4 freeform `----:com.apple.iTunes:*` —
  пользовательские (custom) контейнеры, их точные имена зависят от теггера.
- Для ASF ключи вида `MusicBrainz/*` даны как proposed: точные официальные имена
  ASF MusicBrainz-полей в этом исследовании не подтверждены.
- Для Matroska имена тегов свободные; официальный набор следует **проверять, а не
  изобретать**. Значения в колонке помечены «проверить».
- **`UFID` / MusicBrainz recording отсутствует в выводе ffprobe**, поэтому
  строка «MB record» для ID3 недоступна текущим probe; запись через обычные
  текстовые теги, если она есть, читается, но UFID — нет.
- Фактические значения после probe могут быть склеены через `;` (Vorbis, MP4),
  усечены до первого (ID3) или последнего (WAV INFO/APE/ASF/Matroska) — см.
  раздел о декодерах.

## 13 утверждённых семейств и где какие теги

- 13 расширений: `.flac`, `.wav`, `.aif`, `.aiff`, `.ape`, `.wv`, `.mp3`,
  `.m4a`, `.aac`, `.ogg`, `.opus`, `.wma`, `.mka`.
- **ID3** применяется в AIFF, WAV и AAC (в дополнение к MP3; для MP4/M4A
  основной tag carrier — metadata atoms, не ID3).
- **APE**-теги применяются в APE, WV (WavPack) и AAC.
- Это карта семейств/расширений, а не утверждение о полном извлечении: реальный
  набор прочитанных тегов ограничен текущим ffprobe-выводом (см. ограничения).

## Остаточные ограничения и открытые пункты

### Позднейшее исследование Go-библиотек (2026-10-09)

> **Пометка obsolete (2026-10-09, см. [B14](#appendix-b-owner-decisions-late-2026-10-09)):**
> рекомендация `github.com/tommyo123/mtag v1.0.2` ниже **устарела
> (superseded)**: владелец выбрал **TagLib** (`go-taglib`) с нормализованными
> возвращаемыми тегами, приняв ограниченный набор тегов и опциональные
> `Properties`. Текст ниже сохранён как исторический факт исследования и не
> является выбранным механизмом.

Готовая универсальная библиотека `github.com/tommyo123/mtag v1.0.2` покрывает
все 13 расширений проекта, включая Matroska/MKA. Лицензия MIT, pure Go,
сторонних runtime-зависимостей нет. Проверялся исходный код указанной версии,
не только заявленное покрытие: [source](https://github.com/tommyo123/mtag/tree/v1.0.2).

- `OpenSource(io.ReaderAt, size, ...)` работает с уже открытым источником,
  без повторного открытия пути; применять его можно внутри анализа.
- Native stores (`ID3v2().Frames`, Vorbis fields, APE fields, MP4 items)
  сохраняют множественные значения и позволяют не выбирать только первого артиста.
- Convenience `Artist()`/`AlbumArtist()` возвращают первое значение; общий
  `Tags()`/`Get()` тоже не является полным многозначным представлением.
  Для adapter нужны native stores и multi-value API, а не эти getters.
- Matroska имеет ограничения: `TagBinary` и язык не извлекаются полностью,
  часть scoped/raw stores не экспортируется. Эти пробелы нужно проверить в
  adapter; наличие библиотеки ещё не доказывает сохранение всех тегов.
- По последнему решению владельца готовые Go-библиотеки используются вместо
  самостоятельного написания готовых алгоритмов. Выбор adapter и интеграция
  в анализ ещё не реализованы; source file I/O в matching запрещён.

### Ограничения исследования

- Сырое хранение тегов сейчас **неполно и не реализовано** как полный raw
  retention; требование сохранять все исходные теги уже одобрено владельцем.
  Нужно выбрать техническое отображение без потери native names и списков.
- Прямой дизайн raw-декодера тегов **не утверждён** до архитектурного ревью.
- Канонические имена Picard не подтверждены (страница 404); сопоставление в
  таблице — proposed.
- Поведение декодеров зафиксировано на исследованной ревизии master FFmpeg без
  закреплённой версии и **не является гарантией для всех сборок**.
- Никакие тесты/приёмка этим документом не заявляются; это не отчёт о проверке
  репозитория.

## Позднейшее уточнение статуса M03 (2026-10-09)

- Чистый **grouping/reader capability** M03 завершён и проверен полным
  `task verify`.
- Это **не** завершение всего плана: engine, БД, providers, автоматический
  matching и UI не реализованы; план остаётся в `todo/`.
- Механизм извлечения тегов уточнён решением [B14](#appendix-b-owner-decisions-late-2026-10-09)
  (TagLib / `go-taglib`).
