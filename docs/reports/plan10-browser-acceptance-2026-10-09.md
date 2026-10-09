# План 10 — локальная browser-приёмка

Дата: 2026-10-09. Проверенная UI-ревизия: `1096317`.
После этой ревизии изменялся backend crash-test, не UI.

## Метод

Временный Playwright/Chrome harness использует существующий cached browser и
Vite. API responses замокированы по generated DTO. Новые зависимости в
репозиторий не добавлены; CI HTTP smoke не запускался и не расширялся.

Harness:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan10-browser-20261009/plan10.mjs`.

## Выполненный прогон

- Невалидная concurrency отклоняется, валидное значение отправляется числом.
- Смена output требует явного подтверждения.
- Регистрация source отправляет явный processing mode, затем scan request.
- Получены восемь light/dark desktop/mobile screenshots во временном каталоге.
- Page errors, console errors и unmocked requests отсутствовали.
- В этом потоке приложение запросило `/api/setup` и `/api/settings`;
  `/api/setup/health` не запрашивался. Vite после прогона остановлен.

## Ограничения

Это UI evidence с mock API, не проверка backend, реальных tools, filesystem,
реальной concurrency или deployment. Screenshots не означают исчерпывающего
визуального совпадения с прототипом. Проверки cleanup/inspector, независимых dirty
drafts и keyboard focus на момент этой записи ещё выполняются; их результат
будет добавлен явно после завершения.

## Дополнительный прогон cleanup/inspector/settings

Временные report/screenshots:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan10-browser-20261009/cleanup-evidence/`.

Chrome подтвердил cancel без POST и возврат фокуса, один cleanup POST с selected
ID, terminal mixed results/safe errors и отсутствие автоматического retry в
интервале наблюдения. Inspector показывает неизвестную историю отдельно от
сохранённых SHA/probe successes. Settings сохраняет соседние dirty drafts.
Light/dark desktop/mobile layouts не имели horizontal overflow.

Обнаружено два failed assertions:

1. Реальный дефект: Tab после Shift+Tab в cleanup dialog переводит focus в BODY.
   Исправление и повторная browser-проверка пока ожидаются.
2. Harness считал attempted SSE GET ошибкой. Независимое ревью кода подтвердило:
   queued POST допускает открытие запроса одновременно с immediate REST read.
   Initiated request не означает установленный stream. Правильная проверка —
   terminal UI независимо от SSE establishment, закрытие subscription и отсутствие
   повторных destructive POST. Harness уточняется; исходный failed assertion
   сохранён здесь как исторический результат, а не скрыт.

## Повторный прогон после focus-fix

Проверена `1096317` с cleanup dialog Tab-boundary patch. Исправленный временный
harness выполнил полный повторный прогон в Chrome на `http://127.0.0.1:4173`:
**ноль assertion failures**. Report и восемь screenshots:
`/private/var/folders/53/d19hgbk92bx678h9hm3s7fm40000gn/T/opencode/plan10-browser-20261009/cleanup-rerun-evidence-20261009/browser-report.json`.

- Focus оборачивается в обоих направлениях; native Escape возвращает focus
  на trigger без POST.
- Cleanup отправляет один POST. Перекрывающийся EventSource GET остаётся
  CONNECTING без open/wake-up, после terminal REST закрывается и отменяется
  один раз. Reconnect и автоматического retry за 1,3 секунды наблюдения нет.
- Candidates refresh выполняется один раз при terminal completion.
- Полный повторный прогон также включает inspector/settings/layout проверки
  предыдущего harness. Исходные failures и промежуточный async-loading assertion
  сохранены во временных historical artifacts, не переписаны как успех.
- Основной агент повторил `task verify` после focus-fix: 241 frontend test,
  Go integration, lint, generation и оба build прошли.

Эта приёмка остаётся mocked-API browser evidence, не backend/filesystem validation.
