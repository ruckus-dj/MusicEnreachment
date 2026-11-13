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

## Проверки `1483ef85`

Независимое ревью Incoming и library diff не нашло блокеров. Обычный полный
commit hook (`task verify`) прошёл; срез отправлен в go-development.
Acceptance-процесс на 18080 штатно перезапущен PID 99546 → 11686 hook-built
бинарником source snapshot `1483ef85`. PostgreSQL-контейнер и 8787 не менялись.

На реальном backend через общую browser-навигацию:

- Artist screen показывает `1 release · 1 position`, Date not specified,
  Show/Hide positions и `Audio available · 1 source · published` в English;
  RU показывает соответствующие подписи с `1 релиз`, `1 позиция`, `1 исходник`.
  English → Русский → English сохранил раскрытую позицию и тот же DOM node,
  `document.lang` изменился, language-only API-запросов нет.
- Media summary и position facts показывают обе локали; длительность остаётся
  `0:12`, имя recording не меняется, Recording ID отсутствует. На 1440×900 и
  390×844 в light/dark targeted surfaces не создали page overflow.
  Дополнительный batched проход проверил все сочетания обеих локалей, размеров
  и тем для artist и media/position surfaces (16 состояний): раскрытые позиции
  сохраняются, Recording ID отсутствует, page overflow нет. Фактические body
  backgrounds — `rgb(242,243,239)` и `rgb(17,21,18)`, не только названия настроек.
- Для третьего сохранённого исходника открыт существующий Local release,
  назначение `1. Third Acceptance Cue` совпало с actual preview position 1 /
  displayed_number `1`; request продолжил использовать прежний provider track ID.
- RU/EN переключён при готовом preview: тот же select node и assignment
  сохранились, preview не исчез, API-запросов нет. После browser Confirm matching
  receipt показал Plan13 Third Album, не UUID, а собственный confirmed notice
  перевёлся вместе с receipt. Публикация не запускалась.
- Созданная только для проверки library-запись
  `bfdefdf2-fde8-4bd4-8198-b93931cda363` удалена через browser Delete artist →
  exact preview → confirmation. Scope содержал один релиз/позицию и ноль CURRENT,
  без blockers; operation `00f26e2a-cb8f-5963-856b-62d00a9dcc17` завершилась
  succeeded. Read-only GET подтвердил 404 для временных artist/release и
  сохранение Local provider artist/release/recording в public search.
- Dark Local matching отдельно проверен read-only preview второй позиции на
  1440/390 и RU/EN: draft/preview сохранены без language-only API-запросов и
  page overflow. Confirmation не нажимался, draft очищен перед уходом.

Все mutations выполнены browser-контролами. API GET служат дополнительными
readback-свидетельствами, не заменяют browser-сценарий. Оба прежних CURRENT и все
три исходника сохранили SHA-256 baseline; третьего физического CURRENT не создавалось.
Материалы — `$TMPDIR/opencode/plan13acceptance/presentation-*`.

Это приёмка исправленного участка, не setup/scan/analysis browser E2E и не
закрытие всего RU/EN/визуального паритета.

Первый CI run `43471426736` остановился только на Windows PostgreSQL cleanup:
API tests вывели PASS, затем `supervisor did not respond to shutdown;
force-terminated without confirming PostgreSQL cleanup`. Остальные платформы
и verify/Compose прошли; публикация контейнера была skipped, поэтому полная CI
не считалась зелёной. После разбора failed-step log повторены только failed jobs
без изменения кода, gates или таймаутов.

Attempt 2 завершился успешно: вся
[CI 43471426736](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43471426736)
зелёная, включая Windows x64 и публикацию контейнера. Инфраструктурный повтор
завершён; это не изменение или обход проверок.

План 13 не закрывается. Эти расхождения не исчерпывают RU/EN и визуальную приёмку.
