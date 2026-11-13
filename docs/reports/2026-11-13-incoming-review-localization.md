# Incoming overview/review: RU/EN

Продолжение открытого плана 13 после `ac58c532`.

## Ограниченный scope

Локализация собственных текстов `IncomingGroupsOverview` и
`IncomingGroupReviewScreen`: headings, labels, доступные имена, layout/position
labels, metadata fallback, own notices/errors. Сохранённые собственные сообщения
становятся semantic descriptors и переводятся при render, а не при записи
state; refresh controls не зависят от поиска русской подстроки в error.

Queries, drafts, preview, confirm/authority intents, scope generations и wire
values сохраняются. Музыкальные данные, пути, IDs, revisions/digests и raw server
diagnostics не переводятся. Local matching copy уже локализован и не переделывается.

Вложенные grouping editor, manual pickers, evidence inspector, track overrides
и operation follower не входят в этот срез: их оставшиеся RU-строки не объявляются
исправленными. Это не завершение всего Incoming RU/EN или полного browser E2E.

## Статус

Реализация четырёх frontend файлов завершена, Biome formatting/import
organization и diff whitespace check прошли. Existing RU assertions используют
явную locale fixture; добавлен один locale-flip сценарий с assignment/preview,
semantic conflict/refresh notice и request counters. Независимое ревью выявило
неоднозначные assignment selectors в новом тесте (fixture содержит два файла);
assertions приведены к тому же первому control, который изменяет сценарий.
Других блокеров review не нашло.
Runtime-проверки текущего дерева ещё не проводились;
зелёная CI предыдущего commit не является его проверкой.
