# План 10 — независимое ревью и границы приёмки

Проверенная ревизия: `1096317` (I11). Дата: 2026-10-09.

## Свидетельства

- Основной агент выполнил `task verify`: generation/drift, lint, Go integration
  с PostgreSQL/River, 241 frontend test и frontend/backend builds прошли.
- Pre-commit повторил quality gate и завершился успешно.
- Независимый reviewer изучил соответствующие участки scan, worker, snapshots
  и output-session coordination. В изученном подмножестве критический дефект
  не установлен. Это не исчерпывающее ревью всех этапов I01–I11.
- Source-invariance regression проверяет оба режима, байты/SHA/size/mtime и
  staged registry/copy lifecycle через реальные filesystem/PostgreSQL/River,
  но с **тестовыми** media executables. Это не real-tool compatibility evidence.

## Ограничения

- Native-platform CI и real-media-tool runtime smoke не выполнялись в этой
  сессии; наличие CI configuration не является результатом CI.
- Browser/visual acceptance для этой ревизии на момент ревью не выполнен.
  Владелец отдельно разрешил временный локальный browser-harness без новых
  зависимостей репозитория; результат будет отдельным свидетельством.
- Владелец отложил расширение CI HTTP smoke. Оно не добавляется и не считается
  выполненным.
- Новый process-kill reset regression пока падает и не является доказательством
  crash recovery. Полная crash-boundary matrix не подтверждена.
- Внутренние исторические `enabled` flags сохраняют read compatibility;
  публичного действия отключения root нет.
- Unix filesystem guarantees ограничены утверждённой trusted-filesystem
  boundary; защита от hostile namespace replacement не заявляется.

## Вывод

Локальная реализация I01–I11 прошла указанный gate. Полная приёмка I12 и перенос
плана в `done/` пока не подтверждены. Последующие результаты оформляются отдельно,
а не приписываются этой ревизии задним числом.

## Последующее свидетельство I12

После первоначального ревью добавлен и исправлен subprocess regression reset:
child передаёт оба expected roots, полностью записывает durable identities и
блокируется до commit; parent проверяет preparing journal и прежние runtime
values, убивает child и дважды выполняет recovery. Проверяются удаление только
owned directories и сохранение чужого каталога/старых output files.
Независимое ревью подтвердило эту ограниченную boundary-проверку и обнаружило
race stderr capture; capture затем защищён mutex. Основной агент повторил
`task verify`: gate прошёл. Это одна boundary, не исчерпывающая process-kill matrix.
