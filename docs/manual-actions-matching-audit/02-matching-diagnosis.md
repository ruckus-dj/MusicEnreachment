# Диагностика текущего matching-пайплайна

## Важное различие: UI-очередь и matcher

Manual Actions добавляет запись в «Нужна проверка», если `processing_state == needs_review` **или** `match_state == needs_review` (`frontend/src/screens/ManualActionsScreen.tsx:26`). Это state-based очередь, а не журнал причин matcher. Поэтому туда попадают уже matched и опубликованные записи, блокировки publication и capability, а не только неоднозначные кандидаты.

В текущем UI также нет серверной пагинации/фильтра: браузер получает весь каталог через `GET /api/library/records` и фильтрует локально. Детальная карточка доступна read-only через `GET /api/library/records/{record_id}`; она содержит source provenance, fingerprints, provider attempts, candidates, decisions, публикации и append-only events (`src/music_ingest/api/app.py:631`).

## Как принимается решение сейчас

`resolve_match()` (`src/music_ingest/matching/scoring.py:92`) строит recording score из AcoustID и release score из MusicBrainz, затем идёт по правилам:

1. `local_only` всегда оставляет review.
2. Уникальное точное совпадение artist+album среди fresh/cached MusicBrainz candidates выбирается автоматически.
3. Уникальное recording-context совпадение выбирается автоматически: artist, title, duration, disc/track number/total и Japan path constraint.
4. Иначе проверяется причина review.
5. Лишь один fresh/cached `MusicBrainzMatch` без блокирующей причины auto-select.

`MatchingRequest` уже содержит artist, album, title, duration, track/disc numbers и source path (`processing/worker.py:324`), а `ReleaseCandidate` способен нести recording/track MBID, dates, country, totals, ISRC, performers и artist-credit variants (`matching/providers.py:195`). То есть часть нужных полей уже приходит, но не используется системно для ранжирования и не показывается как объяснение.

## Текущая формула и её следствия

Release score: artist exact-normalized = 0,4; album exact-normalized = 0,4; duration ±2 sec = 0,2, ±5 sec = 0,1 (`scoring.py:291`). Порог по умолчанию — 0,70.

Проблемы этой формулы:

- **Невзвешенный edition context.** Track/disc position, totals, country, date, barcode/catalog number, ISRC и version labels практически не создают разрыва между releases.
- **Нет margin.** 100% vs 100% и 100% vs 1% выглядят одинаково для решения; top score не является вероятностью правильности.
- **Album-centric score путает recording и release.** Один recording законно встречается на promo, compilation, soundtrack и single.
- **Нормализация полезна для поиска, но опасна для identity.** Пропуск пунктуации/регистра не должен стирать distinction `live`, `instrumental`, `edit`, `remaster`, `acoustic`.
- **LidarrContext в модели предусмотрен, но worker его не передаёт** в `_matching_request()`; это кодовая гипотеза потерянного сигнала, не измеренная причина 305 случаев.

## Причины review, реализованные в коде

`ReviewReason` включает `local_only_requested`, `manual_mbid_unverified`, `musicbrainz_stale`, `musicbrainz_ambiguous`, `musicbrainz_unavailable`, `musicbrainz_unknown`, `conflicting_context`, `insufficient_release_score`, `unsafe_text` (`scoring.py:31`). Их приоритет: stale → unsafe → unverified ID → Lidarr conflict → low score → freshness (`scoring.py:310`).

Но `MatchResult.review_reason` не сохраняется как отдельное поле: worker оставляет generic `analysis_ready_for_review`, а candidate evidence сохраняет final score и tags, без порога, component scores, размера пула и проигравшей оценки. Следовательно, причина каждого из 305 случаев сейчас требует реконструкции из деталей, а текущая главная метрика смешивает причины.

## Наблюдаемые root causes и классификация уверенности

| Вывод | Статус | Доказательство |
|---|---|---|
| 260 — неоднородная operational queue, не 260 failed matches | Измерено, live snapshot 14:10–14:11 MSK | 248 `needs_review` + 12 `blocked_infrastructure`; 1 448 records всего. |
| 248 review rows — завершившиеся safe matching outcomes, не active worker backlog | Измерено | Все 248 имеют `processing_state=needs_review`; representative details содержат `ambiguous`, `ratelimited`, multiple candidates или strict recording-confirmation gate. |
| Один `musicbrainz_analysis` не закрывается более 43 минут | Измерено; причина не подтверждена | Noize MC `attempt_count=8`, state `running`; API не отдаёт latest attempt timestamps/state либо heartbeat. |
| Отсутствующий publication/Final workflow заметно раздувает очередь | Измерено | 56 matched+absent; карточка Breaking the Habit. |
| Edition ambiguity является реальной частью хвоста | Измерено на примере, доля неизвестна | We Made It: много 100% releases после AcoustID recording. |
| Variant labels создают recording ambiguity | Измерено на примере, доля неизвестна | Хаос vs Хаос (hard version) по 97%. |
| Недостаток release features/margin увеличивает review | Кодовая причина, нуждается в измерении эффекта | Текущая формула score. |
| Отсутствующий Lidarr context ухудшает disambiguation | Гипотеза | Контекст определён, но не передан worker-ом. |

## Что дополнительно запросить

### Внешние источники, в безопасном порядке

1. **Embedded MusicBrainz IDs и ISRC.** MBID — устойчивый идентификатор сущности; ISRC идентифицирует sound recording, но не обязан совпадать у remix/remaster. Всегда сверять с остальными evidence.
2. **AcoustID/Chromaprint.** Генерирует recording candidates из аудио. Это не абсолютное доказательство: AcoustID coverage пользовательская, а Chromaprint рассчитан на near-identical/full-file audio.
3. **MusicBrainz recording/release relations.** Для recording expansion запросить releases, media, track position, artist credits, duration, country/date, barcode, catalog number, ISRC. Запрашивать кэшированно и с лимитом MusicBrainz: meaningful User-Agent, максимум примерно один запрос в секунду.
4. **Lidarr context.** Оригинальные artist/release/track IDs и quality/file metadata, когда intake действительно пришёл из Lidarr. Это corroboration, не override raw evidence.
5. **Cover Art Archive.** Только review aid для подтверждения издания; не идентификатор аудио.
6. **Локальная album-level консистентность.** Уже подтверждённые соседние дорожки того же source release могут подтвердить edition. Нельзя применять cross-source path как identity; использовать только согласованный набор immutable source observations.

## Источники

1. MusicBrainz Identifier: https://musicbrainz.org/doc/MusicBrainz_Identifier
2. MusicBrainz ISRC: https://musicbrainz.org/doc/ISRC
3. AcoustID Web Service: https://acoustid.org/webservice
4. MusicBrainz Picard, AcoustID tutorial: https://picard-docs.musicbrainz.org/en/latest/tutorials/acoustid.html
5. MusicBrainz Recording Search: https://musicbrainz.org/doc/MusicBrainz_API/Search/RecordingSearch
6. MusicBrainz API rate limiting: https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting
7. Chromaprint project: https://github.com/acoustid/chromaprint
8. Cover Art Archive API: https://musicbrainz.org/doc/Cover_Art_Archive/API

## Безопасностная граница

Никакая рекомендация не должна менять, удалять или переименовывать incoming source. Evidence остаётся append-only; повторный ответ провайдера — новая observation; публикация остаётся staged, hash-validated и atomic. Сомнительный результат не должен становиться auto-selected только ради метрики покрытия.
