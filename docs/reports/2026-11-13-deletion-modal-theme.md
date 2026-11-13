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
