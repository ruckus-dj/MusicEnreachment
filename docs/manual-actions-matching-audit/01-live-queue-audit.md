# Повторный live-разбор: почему конкретные треки не доезжают до единого результата

Дата: 14 августа 2026 г. Все наблюдения ниже получены read-only через живые карточки и `GET /api/library/records/{record_id}`. Ни один source, candidate или publication не изменялся.

Этот документ заменяет прежнюю поверхностную группировку. Главная ошибка прежнего отчёта: он смешал «сколько записей в review» с вопросом «почему этот конкретный recording/release не выбрался». Ниже — причинная цепочка по указанным вами примерам.

## Общая диагностическая картина

### Live-снимок 14 августа 2026, 14:10–14:11 MSK

Наблюдение выполнено read-only через `http://192.168.64.2:8787/workers`, `manual-actions`, `GET /api/workers/queue`, `GET /api/library/records` и пять read-only detail-запросов. Source files, provider evidence, jobs и publications не изменялись.

| Поверхность | Измеренный факт | Вывод |
|---|---|---|
| Каталог | 1 448 `LibraryRecord` | База существенно больше ручной очереди. |
| `manual-actions` | 260 track rows: 248 `needs_review`, 12 `analysis-error` | Это две state-based категории, а не очередь worker’ов. В снимке категории не пересекаются. |
| 248 review rows | Все имеют `processing_state=needs_review`; 57 из них также `match_state=needs_review` | Очередь создана завершившимся безопасным отказом auto-selection, а не текущим retry. |
| 12 error rows | `processing_state=blocked_infrastructure`, `match_state=unmatched`, publication `current` | Это отдельный infrastructure cohort, требующий разбор shared dependency до bulk retry. |
| Worker queue | Одна `musicbrainz_analysis`, `running`, `attempt_count=8`, создана в 13:28 MSK; всё ещё `running` в 14:11 MSK | Задача старше 43 минут и не закрылась, но API не возвращает начало/состояние latest attempt или heartbeat. Нельзя честно назвать её abandoned вместо долгого provider call. |

Задача связана с `Noize MC — Песня для радио` (`The Greatest Hits Vol.1`), source `b910…f58`, `LibraryRecord record-8f58…`. Текущие runtime settings отдают `timeout_seconds=10`, `retry_delay_seconds=30`, `max_attempts=3`, `worker_concurrency=4`. Поэтому восемь сохранённых попыток при текущем лимите — сигнал для проверки durable `job_attempts` и worker logs, а не основание для ручного изменения job row.

**Чего пока нельзя утверждать.** UI видит только durable `JobRecord.state=running`, число attempts и конфигурацию. Для подтверждения stale lease нужны latest `JobAttemptRecord.state`, `started_at`, `finished_at`, а также worker heartbeat/last poll. Без этих полей нельзя безопасно requeue или считать provider причиной зависания.

### Почему 248 не означают, что анализ «не улучшился»

Пять проверенных representative records имеют publication `current`, но остаются reviewable из-за безопасного matching gate:

- `Anacondaz — Хаос feat. Артем Пивоваров [hard version]`: несколько AcoustID candidates и `stored_candidates_auto_selected`, но recording не закреплён.
- `Linkin Park — We Made It (Album Version)` и `We Made It (Instrumental)`: MusicBrainz `ambiguous`; у первого оператор уже подтвердил AcoustID recording.
- `Linkin Park — [PTS.OF.ATHRTY] (Single Edit)` и `H! VLTG3 (Single Edit)`: provider candidates существуют, но safe automatic match не проходит; в одном случае присутствует AcoustID `ratelimited`.

Это подтверждает существующий вывод: improvement для Noize устраняет конкретный album-compatible AcoustID case, но не снимает edition ambiguity, variant conflict, strict recording confirmation и provider-capability outcomes для остальных cohorts. Автоматически снять 248 флагов без нового доказательства было бы unsafe auto-selection.

### Безопасный порядок продолжения

1. Добавить read-only worker status detail: latest attempt timestamps/state, failure reason, `last_poll_at` и slot heartbeat; затем сравнить Noize job с 5-minute lease policy.
2. До массового retry открыть representative detail из каждого cohort (`needs_review`, `blocked_infrastructure`, `ratelimited`, `ambiguous`) и устранить общую capability/infrastructure причину, если она есть.
3. Проверить один source per cohort через существующий scoped retry/reprocess. Только после успешного sample использовать bulk endpoint.
4. Оставить ambiguous/conflicting recordings в review до появления recording/release split и album-level resolver из roadmap ниже.

Текущая система смешивает три независимые операции:

1. **Recording identity**: какой MusicBrainz recording соответствует аудио.
2. **Release edition**: в каком конкретном CD/digital/promo release этот recording должен быть представлен в вашей библиотеке.
3. **Effective source/publication**: какой из нескольких исходных файлов должен стать единственной актуальной managed-копией.

В коде и UI они частично независимы, но итоговое состояние одно — `needs_review`. Это порождает четыре наблюдаемых дефекта:

- AcoustID recording не закрепляется, пока MusicBrainz уже не вернул однозначный `musicbrainzmatch` с тем же recording; поэтому «один очевидный AcoustID» всё ещё выглядит как невыбранный.
- release может сохраниться без recording, а recording — без release.
- нет aggregate, который выбирает одну edition для всех треков наблюдаемого альбома;
- source selector уже умеет выбрать один source и atomic-replace публикацию, но скрывает полные пути и историю и не объясняет, почему другой source отсутствует/не подходит.

## 1. Noize MC — «Песня для радио»: два похожих файла и отсутствие склейки

### Наблюдение A: MP3 остаётся отдельным record

Карточка `record-9ec74d2983c84da2a763605fd30bff5b` содержит ровно один source:

```text
/data/downloads/music/Noize MC (Дискография)/2008 - The Greatest Hits Vol.1/01 - Песня Для Радио.mp3
```

Live evidence:

- два AcoustID recording-candidate: `47d13484…cd77` («Песня для радио», *The Greatest Hits Vol.1*) и `48c984ee…2a62` («Песня для радио (полная версия)», *The Greatest Hits Vol.2*);
- у обоих одинаковый score `0.96927744` (97%);
- единственный показанный MusicBrainz release-candidate — *The Greatest Hits Vol.2*, score `0.4`;
- source album/path говорит *Vol.1*;
- event: `analysis_ready_for_review: AcousticID and MusicBrainz analysis completed without a safe automatic match`.

**Почему не выбран «правильный по альбому» AcoustID?** В текущем коде уникальность определяется только после проверки кандидата через MusicBrainz (`ProcessingWorker` и `RecordingAssociationService`). Для двух одинаково квалифицированных AcoustID recording-candidates нет правила «выбрать recording, чьи release включают exact source album». Поэтому source album существует в evidence, но не превращён в tie-breaker recording decision.

**Что должно быть:** развернуть оба AcoustID recording MBID в MusicBrainz и сравнить их release membership с immutable `ALBUM`, `TRACKNUMBER`, `TRACKTOTAL`, `DISCNUMBER`, duration и опциональными catalog/country hints. Здесь exact `The Greatest Hits Vol.1` должен дать recording `47d13484…cd77` преимущество над Vol.2. Это не эвристика по имени файла: album tag и track layout — evidence, а path лишь объяснение оператору.

### Наблюдение B: FLAC — отдельный успешно подтверждённый record

Карточка `record-2352eb88bffa4d379829172408cc7b13` сейчас содержит source:

```text
/data/incoming/Noize MC/01 - Песня для радио.flac
```

и находится в здоровом состоянии: recording `47d13484…cd77`, release `d5c9ba44…c19d`, `processing=complete`, `match=matched`, `publication=current`, Final rev 3. История содержит `source_recording_reassigned`, `selection_refresh_selected`, затем полный publication attempt и `final_published`.

Это доказывает, что **переключение effective source и замена managed-публикации уже реализованы**: выбирается один source, старый publication становится `superseded`, новый — `current`; аудио в managed-root заменяется staged/atomic flow. Не должно быть двух текущих публикаций одного recording.

### Проверка PostgreSQL: что произошло с двумя FLAC

Read-only запрос к test_stand PostgreSQL дал точный ответ:

| Source | root | device / inode | SHA-256 | Текущий LibraryRecord | Recording |
|---|---|---|---|---|---|
| `/data/downloads/music/.../tracks/01 - Песня для радио.flac` | `Downloads` | `37 / 416` | `db38c760…9b81ad5` | `record-8f58…` | `48c984ee…` (Vol.2) |
| `/data/incoming/Noize MC/01 - Песня для радио.flac` | `Incoming` | `37 / 416` | `db38c760…9b81ad5` | `record-2352…` | `47d13484…` (Vol.1) |

Это два разных `SourceRecord`, но они указывают на один inode и одни байты: Docker bind-mount сделал один файл доступным по двум путям. Source ID намеренно содержит `source_root_id`, device, inode **и** SHA-256, поэтому различие roots создаёт две immutable observations, а не один source.

Они действительно были склеены на уровне record: history incoming-source содержит `content_sha256_consolidated` migration. Затем автоматическая association ошибочно/независимо перевела Downloads observation в другой record по `48c984ee…` (Vol.2), а Incoming оставила с `47d13484…` (Vol.1). Поэтому UI каждой карточки показывает один source: source не исчез, он был **расщеплён поздней recording-association**, несмотря на равный content hash.

Это подтверждает отдельный P0 дефект: exact-content duplicates нельзя разъединять в разные recordings только из-за противоречивого provider candidate без явного conflict resolution. При одинаковом SHA-256 и fingerprint association должна либо удержать оба source на canonical record, либо создать `content_identity_conflict` для review; она не должна молча отправлять один и тот же audio blob в два разных recording aggregates.

### Почему есть `/data/sources/legacy`

Это не третий Noize source. В запущенном test stand явно установлены `MUSIC_INGEST_INCOMING_ROOT=/data/sources/legacy`, `MUSIC_INGEST_SOURCE_ROOTS_PARENT=/data/sources` и `MUSIC_INGEST_E2E_SEED_ENABLED=true`. Таблица `source_roots` содержит root `legacy`, а под ним только два E2E fixture source (`e2e-source-a.flac`, `e2e-source-b.flac`); ни один не относится к Noize. Это тестовая конфигурация/migration compatibility root, который не должен участвовать в production reasoning о текущих `Incoming`/`Downloads` observations.

## 2. Anacondaz — один AcoustID 100%, но recording не выбран

`record-3cad1038d13a4cff847c0cf2210f32e9`, source `Спаси, но не сохраняй.flac`:

- один AcoustID candidate `9f24f90c…b6196d` со score `0.99703324` (100% UI);
- два MusicBrainz release-candidate *Выходи за меня*, по `0.8` (CD/Digital edition ambiguity);
- event `recording_association_review_required: automatic recording lacks confirmed MusicBrainz recording evidence`;
- MusicBrainz attempt outcome — `ambiguous`; publication уже `current`.

**Точная причина:** `RecordingAssociationService.associate_automatic()` требует не только score ≥ threshold, но и `_has_confirmed_recording()`. Последняя требует одновременно MusicBrainz provider attempt с outcome `musicbrainzmatch` и candidate evidence с тем же `MUSICBRAINZ_TRACKID`. При `ambiguous` это условие ложно, даже если AcoustID дал единственный 0.997 candidate. То есть safety gate ошибочно делает выбор **recording** зависимым от выбора **release**.

**Исправляемая политика:** verification recording MBID — отдельный lookup конкретного recording MBID в MusicBrainz, не search результата release. Если MBID существует и source metadata (artist/title/duration) не конфликтует, закрепить recording append-only. CD/Digital release оставить отдельному resolver-у. Это безопаснее, чем считать ambiguity release недоверием к audio fingerprint.

Для пары CD/Digital, где весь track layout и recording membership совпадают, нужна deterministic policy: выбрать canonical edition по явно зафиксированному ранжированию (например, Digital при отсутствии physical evidence; или CD при barcode/catalog number/CD path evidence), записать `equivalent_release_set` и rationale. Если все разрешённые fields идентичны, выбор первого **в детерминированном сортированном порядке MBID** допустим как canonical projection, но только после проверки, что releases эквивалентны для данного альбомного набора, а не просто одинаково названы.

## 3. Release выбран без recording: Anacondaz и Linkin Park

### Anacondaz «Хаос feat. Артем Пивоваров [hard version]»

`record-5813e8188bd24e6b9a2da047ee3a467a`:

- release `f7f90d79…3e18` (*Выходи за меня*) уже сохранён;
- recording отсутствует;
- два AcoustID candidates по `0.9671348`;
- `processing=needs_review`, `match=needs_review`, publication `current`;
- history: `stored_candidates_auto_selected`.

Это не нормальное «завершённое» состояние. Current worker умеет применить stored release candidate отдельно, но automatic recording association остаётся под строгим MusicBrainz-confirmation guard. В результате release ставится, а recording не закреплён.

### Linkin Park «H! VLTG3 (Single Edit)»

`record-8ab1091d97314bb288d595875357c461` демонстрирует обратный риск:

- release *Pts.of.Athrty* выбран;
- три AcoustID recording-candidate: два около 97% из других альбомов и один 74,9% из *Pts.of.Athrty*;
- record остаётся `needs_review`.

Здесь release membership должен быть **hard compatibility filter** при выборе recording: если в уже выбранном album edition существует ровно один compatible recording/track, он приоритетнее глобального AcoustID top score, когда версия/title/duration/track position согласуются. Однако нельзя применять это вслепую: если high-score candidate реально соответствует другой audio version, а выбранный release не содержит совпадающего track, album resolver обязан оставить conflict review.

### Правильная единица автоматизации

Нельзя выбирать release на уровне одной дорожки и надеяться, что остальные сами придут к тому же. Нужен отдельный **Release Resolution Group**:

- это не path и не `LibraryRecord`;
- она объединяет source observations по album artist/title + track/disc membership + подтверждённым recordings;
- хранит candidate editions и append-only decision;
- auto-select только release, совместимый со всеми доступными членами группы;
- при недостающих/конфликтных треках оставляет группу review, не выбирая half-album;
- после выбора создаёт final metadata revisions и selection-refresh для всех member `LibraryRecord`.

Так 18 дорожек попадут в один выбранный MB release либо вся группа будет обозначена как конфликтная; Navidrome не получит половину альбома из CD и половину из Digital.

## 4. Busta Rhymes feat. Linkin Park — recording есть, edition scoring нет

`record-4f397722999743799349bb21e9b1f9ba`, «We Made It (Album Version)»:

- reviewer уже выбрал AcoustID recording `5eb8e3dc…27cf4` (score `0.9639372`);
- source tags: album `We Made It [Maxi Single]`, track `1/3`, date 2008; source directory добавляет `Japan WPCR-12973`;
- MusicBrainz предлагает много releases с UI score 100%: single/promo/compilation/soundtrack;
- release не закреплён; имеются прошлые `flac analysis unavailable` и повторные analysis cycles.

Текущий release score использует только normalized artist (0.4), release title (0.4), duration (0.2). Он не сравнивает полный tracklist источника с MusicBrainz medium, не использует `1/3`, country `JP`, catalog `WPCR-12973`, barcode, release date/type, track title version или согласованность соседних tracks. Поэтому score не различает editions.

**Нужный candidate scorer для release group:**

| Сигнал | Роль |
|---|---|
| recording MBID содержится в medium | обязательный фильтр |
| album artist/title | сильный score, не единственный |
| disc/track number + total | сильный score |
| весь наблюдаемый tracklist группы против medium tracklist | самый сильный edition discriminator |
| duration каждого совпавшего трека | corroboration |
| catalog number/barcode/country/date/type | edition tie-breaker |
| `Japan` и `WPCR-12973` из source observation | country/catalog evidence, не identity path |
| version labels (album/radio/single/edit/live/instrumental) | hard conflict либо penalty |

Для этого примера ranking должен отфильтровать soundtrack/Greatest Hits, предпочесть Japanese 3-track release с catalog number; если остаётся несколько эквивалентных copies, применить deterministic canonical policy и сохранить equivalent set для UI.

## 5. кис-кис — AcoustID `nomatch`, MusicBrainz `malformed`

`record-1f79c81ce5f34de29da87418296cb88c`, «здоровью.нет»:

- fingerprint сформирован, но AcoustID outcome `nomatch`;
- MusicBrainz outcome `malformed`, поэтому UI не показывает ни одного candidate;
- исходные tags сильные: artist `кис-кис`, album `Как перестать беспокоиться и начать жить`, title `здоровью.нет`, date 2022, track 5;
- указанный вами MB mirror подтверждает recording `76bbfdd6-49d2-449d-822c-d942b362a803`, artist, length 3:00, year 2022 и ISRC `FRX282275254`.

AcoustID nomatch не означает, что recording не существует: fingerprint может не быть связан с AcoustID или ответ не покрывает конкретный master. Здесь нужен **text-first fallback**, а не ручной поиск:

1. MusicBrainz recording search по quoted artist + recording title; затем по artist + release + track number; затем transliteration/normalization variants.
2. Для каждого candidate загрузить recording/release/media и score по artist, title, album, track/disc layout, duration и release year.
3. Если один recording/release group проходит calibrated threshold и margin — выбрать автоматически; иначе представить ranking оператору.
4. Если с verified candidate приходит ISRC, добавить его только в analyzed/final metadata revision с provenance; исходный tag не переписывать.
5. `malformed` должен показывать в UI HTTP status/request descriptor/response snippet hash и ставить retryable provider-error отдельно от `no candidate`.

Эта стратегия также покрывает весь класс «нет AcoustID», а не только кис-кис.

## Что меняется в приоритетах

1. Сначала починить split recording/release decision и audit lost/misassociated sources.
2. Затем добавить album-level release groups и group scoring.
3. Затем text/ISRC fallback для `AcoustID nomatch` и нормальную диагностику `MusicBrainz malformed`.
4. Только после shadow-mode evaluation включать automatic canonicalization эквивалентных CD/Digital editions.
