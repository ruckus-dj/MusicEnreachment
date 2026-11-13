# Deletion modal: тема и доступность действий

Следующий ограниченный срез открытого плана 13 после `1c0a7f6c`.
Реальная browser-проверка выявила белый фон Modal при светлом foreground в dark
mode. Свидетельство — [refresh report](./2026-11-13-deletion-preview-refresh.md).

## Scope

Presentation-only port существующих theme tokens и modal/scroll/footer паттерна
самостоятельного прототипа. Fixed light colors заменяются surface/ink/muted/
line/control/error; высота содержимого ограничивается viewport, действия
сохраняют доступность при прокрутке на mobile. Refresh, frozen digest,
uncertain replay, scope guards и подтверждение не перерабатываются.

## Статус

Реализация завершена в `LibraryDeletionControl.tsx`: theme tokens/AppButton,
bounded flex modal, отдельный scroll content и shrink-free footer. Поведение
удаления/refresh не менялось. Biome и diff whitespace check прошли; независимое
ревью выявило потерянные overlay positioning/stacking/backdrop classes.
Они восстановлены; повторное независимое ревью не нашло оставшихся блокеров.
Runtime-проверки текущего дерева ещё не проводились;
зелёная CI предыдущего commit не является проверкой нового presentation.
План 13 остаётся открытым.

## Проверки `93073a42`

Обычный полный hook прошёл, commit отправлен; CI `43498110078` выполняется.
Acceptance-only процесс штатно обновлён PID 35204 → 38996; PostgreSQL и 8787
не менялись. Реальный dark modal теперь имеет surface `rgb(25,30,26)` при
foreground `rgb(227,234,227)`, а не белый фон. На mobile 390×844 его размеры
366×590.8, top 126.6; footer и все три действия внутри viewport, content
scrollHeight 742 при clientHeight 432. Проверены также RU/EN и 1440×900:
page overflow отсутствует, actions доступны после прокрутки.

Screenshot просмотрен. Обнаружена дополнительная same-slice поправка:
AppButton base перекрывает error text token destructive кнопок, поэтому
приоритет danger classes уточняется по существующему prototype pattern.
До повторной проверки computed danger colors визуальный срез не объявляется
полностью принятым. Confirm удаления baseline не нажимался.
