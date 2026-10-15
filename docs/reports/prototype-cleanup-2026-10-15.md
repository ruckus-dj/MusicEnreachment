# Cleanup неопубликованного прототипа

Дата: 2026-10-15. Проверенная кодовая ревизия:
`500e57fe7c695c045bae037abd33ce868f1ef3b6`.
Исходная ревизия до cleanup:
`222af36c8b58e6f955c6b0e3736fecd7462ead20`.

## Результат

- 80 пар прикладных SQL-миграций заменены пятью baseline-парами:
  `application_core`, `source_inventory_analysis`, `provider_cache`,
  `local_library`, `incoming_matching`. Они создают конечную схему напрямую,
  без исторических backfill и промежуточных определений.
- Удалены старый `tools.Manager`, service-обёртки `SaveRuntime`/
  `SaveRuntimeRequest`, fixture-only executor анализа и чтение прежнего
  singular-ключа Setup в localStorage. Дедупликация текущего ключа сохранена.
- Matching читает только текущие calculation receipts `3/3/3` и search digest
  version `2`. Recovery/replay, текущие digest/seal checks, fencing и утверждённая
  математика scoring сохранены. AcoustID требует атомарного credential store.
- Исторические upgrade/backfill/old-schema rollback tests заменены проверками
  текущей схемы там, где сохраняется действующий инвариант. В частности,
  сохранены прямые проверки NULL execution tuples, неизменяемости execution mode,
  Unicode whitespace в policy и корректного retirement актуальных proof graphs.

## Проверки

- Полный `task verify` прошёл: generation/drift, Go/ frontend lint,
  Go unit/integration с изолированными PostgreSQL/River fixtures,
  **299 frontend tests**, Node tools tests, frontend/backend build.
- Pre-commit повторно выполнил полный gate и завершился успешно перед созданием
  указанной кодовой ревизии. Hook не отключался.
- Временный integration probe внутри полного gate применил старые 80 миграций
  и новые baseline к разным изолированным БД PostgreSQL 17. Сравнены
  **1623 записи каталога** для каждой схемы, включая 85 прикладных таблиц,
  columns/defaults, CHECK/FK/deferrability, indexes, function definitions,
  triggers, sequences и обязательный grouping seed. Строковые определения
  сравнивались точно, без сворачивания пробелов или управляющих символов.
- Найдены только пять ожидаемых отличий: три calculation-version CHECK теперь
  допускают только `3/3/3`, search-version CHECK допускает только `2`, а его
  default равен `2`. Других различий в проверенном каталоге нет.
- Постоянные integration tests проверяют fresh apply, idempotence, clean group
  rollback/reapply и отказ unsafe rollback без изменения данных и migration
  status. Отдельный тест применяет настоящие миграции River и подтверждает
  сохранность его job/history и посторонней таблицы при application rollback.
- Выполнены независимые review runtime cleanup, matching formats, SQL baseline,
  документации и сохранения schema regression coverage. Найденные замечания
  исправлены до финального gate.

Временный probe и исходные 80 SQL-файлов для сравнения в репозиторий не включены.
Локальные catalog evidence сохранены в approved temporary directory
`cleanup-before/`; SHA-256 файлов:

- `old-exact-catalog.json`:
  `083487baa757c8901cbe35d91026eac8d73d5aa3b63f01e0bf8475b56c6b18f9`;
- `baseline-exact-catalog.json`:
  `bf6cca2055d58f209172a8cdec2af66fb3119a576ac0ac901afefc78172be10d`.

## Границы

Это fresh-instance reset, а не upgrade опубликованных БД. Существующие
пользовательские БД и `test_stand/` не изменялись. Baseline down отказывается
удалять заполненные прикладные таблицы; Bun/River schema ownership разделено.
Проверки выполнялись локально на macOS arm64; GitHub platform matrix и новая
browser-приёмка этим отчётом не заявляются. Файловая публикация не реализовывалась.

Актуальные design-документы и AGENTS.md консолидированы отдельно; исторические
завершённые планы, аудиты и прежние отчёты не переписывались.
