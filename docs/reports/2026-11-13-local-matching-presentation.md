# Local matching: нумерация, подписи и RU/EN

Продолжение открытого плана 13 после `75673d31`. Предыдущие browser-свидетельства
и границы — [contributor projections](./2026-11-13-contributor-projections.md).
Продуктовые правила не меняются; текущий контракт — `../design/decisions.md`.

## Подтверждённые расхождения

- Local provider catalog возвращает zero-based `track.position`; matching
  preview возвращает one-based position. Assignment label читал raw catalog
  position и показывал `0.` для первой позиции, хотя preview давал position 1.
- Внешний summary Local matching описывал снятый direct-create workflow.
  Фактический поток уже provider-mediated: выбор существующего Local release →
  preview → явное matching confirmation.
- Receipt показывал внутренний library release UUID вместо названия релиза.
- Карточка позиции показывала внутренний Recording ID; карточки артиста/релизов
  смешивали русские подписи с English.

Исправления ограничиваются presentation. Provider/library identities, stored
positions, authority/revision fences, inputs запросов и правила публикации
сохраняются. Standalone prototype остаётся графическим эталоном, не runtime
зависимостью production.

## Local matching

Assignment label использует непустой trimmed `displayed_number`, иначе
`position + 1` из raw Local catalog. Сами значения позиции и provider track ID
не изменяются. Receipt показывает `release_title` из принятого response, а не
текущего изменяемого выбора или UUID. Summary берётся из RU/EN-каталога.
Собственные confirmation/scope notices хранят semantic keys и переводятся при
render; raw diagnostics сохраняются. Переключение языка не должно сбрасывать
назначение/preview или повторять catalog/detail/preview requests.

Расширены существующие frontend assertions, без новых DB-сценариев. У standalone
prototype нет отдельного аналога этого Local matching receipt/disclosure;
его Local provider screen редактирует каталог. Поэтому новые искусственные
prototype workflow/fixtures для этих исправлений не добавляются.

## Карточка артиста и факты релиза/позиции

Локализованы labels/fallbacks, loading/error/empty states, pagination и ARIA names
в LibraryArtistScreen, а также ReleaseMediaSummary и PositionIdentity.
Числа и их агрегация не меняются; release/position/source count labels используют
соответствующую локаль и plural forms. Business-значения названий, дат, форматов
и длительности не переводятся. Recording показывается как «Композиция» в RU,
а строка внутреннего Recording ID удалена только из presentation.

Это следует уже существующим RU/EN catalogueMessages/libraryRowsMessages
standalone prototype; новые demo data или workflow не вводятся. Главный
LibraryScreen и PositionSources в этот ограниченный срез не входят: их оставшиеся
RU/EN строки и source variant UUID не объявляются исправленными.

Biome formatting/import organization выполнены из frontend workdir; запуск
frontend binary из корня сначала выявил конфликт nested root configs, исправлен
только рабочий каталог вызова, не конфигурация. Механический design detector
на четырёх изменённых production TSX вернул пустой список findings.

План 13 не закрывается. Эти расхождения не исчерпывают RU/EN и визуальную приёмку.
