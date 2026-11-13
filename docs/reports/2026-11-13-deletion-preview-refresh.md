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
