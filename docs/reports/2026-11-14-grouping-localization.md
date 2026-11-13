# Incoming grouping controls: RU/EN

Следующий ограниченный срез плана 13 после `c7179622`.

## Scope

Собственные тексты `IncomingGroupingEditor`: labels, headings, доступные имена,
counts, действия, notices/errors. Сохранённые сообщения имеют semantic identity
и переводятся при render; server detail и неизвестные `Error.message` остаются
без перевода. Отдельный `incomingGroupingMessages.ts` следует существующему паттерну
Local matching messages.

Sequence guards, stale/409, needs-preview gating, selections, draft actions,
stable action row IDs, draft revision и awaited `onConfirmed` не меняются.
Locale не вызывает запросов или сброса состояния. Raw kind/ID в draft action
summary сохраняются в этом срезе; их display-only presentation mapping требует
отдельного разбора, а не изменения wire identity при переводе.

## Статус

Реализация трёх frontend файлов завершена и отформатирована; независимое
ревью выполняется. Один fixture test покрывает draft/preview/selection при
смене языка, собственный fallback и raw 409 detail. Runtime-проверки ещё не проводились;
зелёная CI предыдущего commit не является его проверкой. Общий план 13 открыт.

Ревью подтвердило сохранение authority/state guards и выявило два блокера:
status text использовался как accessible name, HTTP fallback содержал лишнюю
точку. Assertions исправлены на text queries, terminal period убирается только
перед собственным HTTP suffix; raw diagnostics и standalone punctuation сохранены.

Первый обычный hook остановился до тестов на Biome organize-imports в grouping
editor. Formatting-only проход не организовал imports; `biome check --write`
с отключённым linter исправил единственный import-order error в трёх scoped
файлах. Gates не менялись; commit не создан. Следующая проверка — повторный
обычный полный hook, не частичный test gate.

## Проверки `3635f18e`

Повторный обычный полный hook прошёл, commit отправлен. Полная CI
`43518840816` выполняется. Acceptance-only процесс 18080 штатно обновлён
PID 59561 → 64931; PostgreSQL и 8787 не менялись.

Browser добавил только merge draft двух групп: RU/EN сохранили одну action row
и перевели notices без API requests. Draft отменён через Cancel draft.
Preview/Confirm не нажимались, группировка backend не изменялась. Preview и
409/error retention покрыты frontend fixtures, не real-backend browser E2E.
Визуальная theme/size приёмка ещё не завершена.

Вся [CI 43518840816](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43518840816)
для `3635f18e` зелёная с первого attempt, включая Windows и публикацию
контейнера. Это не заменяет оставшуюся визуальную и сквозную приёмку.

Дополнительный browser проход открытых grouping controls на текущем бинарнике:
RU/EN × light/dark × 1440×900/390×844, headings доступны и page overflow нет.
Dark tab перед повторной проверкой явно перезагружен, чтобы не принять старый
frontend snapshot за текущий. Это idle/draft presentation, не подтверждённая
перегруппировка или real-backend preview/conflict E2E.
