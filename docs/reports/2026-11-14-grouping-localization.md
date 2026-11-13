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
