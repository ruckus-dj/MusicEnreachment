# Явное обновление deletion preview

Продолжение открытого плана 13 после `f8ca7b63`.

Browser-приёмка reorder выявила retained blocked preview: после окончания
reconciliation закрытие/открытие диалога не давало свежего preview, требовался
reload страницы. Прежнее свидетельство и завершённый cleanup —
[отчёт reorder](./2026-11-13-contributor-row-order.md).

## Ограниченный scope

Явная RU/EN кнопка обновляет preview прежним no-store запросом до подтверждения.
Во время обновления Confirm недоступен. После uncertain confirmation или
получения operation refresh не предлагается: frozen digest, replay и retry
сохраняются. Backend/API/authority checks не меняются.

## Статус

Реализация завершена; независимое production-ревью не нашло блокеров.
В существующем regression сохраняются deferred refresh, запрет confirm во время
запроса, новый digest и прежний uncertain replay без повторного preview read.
Первый fixture уточняется как blocked → clean, чтобы покрывать реальный случай,
а не только обновление уже чистого preview. Runtime-проверки ещё не запускались;
зелёная CI `43487504866` относится к предыдущему commit.
План 13 остаётся открытым.

Первый обычный hook остановился на существующем deferred-refresh genre test:
Edit нажимался до завершения initial loading, затем ожидаемая clear-кнопка
не появлялась. Backend и новые deletion-refresh проверки прошли. В тесте
добавлено ожидание enabled Edit и enabled editing control до запуска deferred
refresh; исходные dirty-draft/conflict assertions сохранены. Production genre
код и таймауты не менялись. Biome проверил три затронутых frontend файла.

## Проверки `1c0a7f6c`

Повторный обычный полный hook прошёл; commit отправлен. Полная
[CI 43494715619](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/43494715619)
выполняется. Acceptance-only процесс штатно обновлён PID 28646 → 35204;
PostgreSQL и 8787 не менялись.

Browser на реальном backend загрузил artist deletion preview двух baseline
артистов и явно обновил его: GET возвращают новый digest с тем же scope одного
CURRENT и без blockers. RU/EN refresh доступен после закрытия/открытия диалога;
проверены light/dark и 1440×900/390×844. Confirm ни разу не нажимался;
отслеживаемые запросы refresh — только GET. Blocked→clean, pending exclusion и
uncertain replay подтверждены frontend regression, не real-backend browser E2E.

Визуальная dark-приёмка **не пройдена**: screenshot и computed styles выявили
белый фон родительского Modal (`rgb(255,255,255)`) при светлом foreground Dialog
(`rgb(227,234,227)`). Mobile dialog также требует проверки доступности footer
при большой высоте. Это отдельный presentation-пробел, не обход authority;
следующий срез готовится read-only до полной CI.

Оба CURRENT и три исходника сохранили SHA-256:
`$TMPDIR/opencode/plan13acceptance/deletion-refresh-hashes.txt`.

Вся CI `43494715619` зелёная, включая Windows и публикацию контейнера.
Это подтверждает функциональный срез, но не отменяет выявленный dark modal
дефект: его исправление и визуальная приёмка идут отдельным следующим срезом.
