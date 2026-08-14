# Аудит очереди «Нужна проверка»

Дата наблюдения: 14 августа 2026 г.  
Поверхность: `http://192.168.64.2:8787/manual-actions` и read-only API `GET /api/library/records`.

В каталоге четыре связанных документа:

1. [01-live-queue-audit.md](01-live-queue-audit.md) — live-снимок queue/manual actions и повторный разбор Noize MC, Anacondaz, Linkin Park, Busta Rhymes и кис-кис.
2. [02-matching-diagnosis.md](02-matching-diagnosis.md) — текущая логика matcher и наблюдаемые пробелы в объяснимости.
3. [03-automation-roadmap.md](03-automation-roadmap.md) — предметный план: recording-first, source audit, album-level release group, edition scorer и fallback без AcoustID.
4. [04-clean-run-2026-08-14.md](04-clean-run-2026-08-14.md) — воспроизводимый чистый прогон, исправленный MusicBrainz parser defect и фактическая разбивка 796 строк.

## Короткий вывод

Live-снимок в 14:10–14:11 MSK показывает 260 ручных действий: 248 `needs_review` и 12 `blocked_infrastructure`; это не 260 незакрытых worker jobs. Одновременно в worker queue остаётся одна `musicbrainz_analysis` для Noize MC «Песня для радио», созданная в 13:28 MSK и всё ещё `running` спустя 43 минуты. Endpoint не отдаёт время начала и состояние последней попытки, поэтому нельзя отличить активный зависший provider call от брошенного lease; это отдельный observability gap.

Главный вывод matching-расследования: текущая модель путает recording identity, release edition и effective source. Это подтверждено live-кейсами: один AcoustID 0.997 остаётся review из-за ambiguous release; release может сохраниться без recording; и source нельзя диагностировать из basename-only selector. Roadmap направлен именно на эти дефекты и на наблюдаемость worker state.

Последний clean run не подтверждает безопасную возможность достичь 99–100% auto-selection
одной настройкой: исправление parser-а убрало 330 ложных `malformed`, но оставило
647 реальных MusicBrainz ambiguities и 344 AcoustID rate-limit outcomes. Подробная
методика и безопасная последовательность улучшений находятся в документе 04.
