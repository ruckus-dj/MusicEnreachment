# Root deletion: связи последней location

Базовая ревизия — `2c81f970f786eba74e0985159ec291cd84c19ae3`,
прошедшая полный hook и [CI 42316624690](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42316624690).

Срез исправляет существующую root-deletion транзакцию: перед каскадным удалением
locations фиксируются затронутые variants, после удаления очищаются только их
`track_source`-связи, если locations больше нет ни в одном root. Сохранена
существующая fingerprint coordination; новые output gates и миграции не нужны.
SHA variants остаются cache, digestless проходят прежний orphan-pruning после
удаления связей. Выбор не переносится на альтернативу; fallback и публикации
не запускаются.

Проверки добавлены для shared roots, SHA/digestless, конкуренции association
writer, сохранения реального CURRENT и unselected альтернативы, подтверждения
и rollback при поздней SQL-ошибке. Полный gate и CI среза пока не выполнены.

## Границы

Это не завершение пункта 2 плана 13: staged copies, failed-task retirement и
общая reset-приёмка остаются отдельно. Publication/cleanup recovery ownership
не удаляется и продолжает блокировать удаление. Scan/enumeration location
retirement этим root-only изменением не расширяется. Последующий явный generic
association upsert к SHA cache не запрещается новой schema-политикой.
