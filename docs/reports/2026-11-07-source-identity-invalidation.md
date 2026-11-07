# Отзыв live identity изменённого source

Базовая ревизия — `9b5725dec44ead2f9617a2243a19735a1dfeb740`;
обычный hook и полностью завершённый
[CI 42357464505](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42357464505)
прошли, включая публикацию контейнера.

## Область продолжающейся работы

Реализован немедленный отзыв live identity при подтверждённом изменении
size/mtime, в том числе во время анализа и до завершения scan. Inventory facts
и generation неполного обхода не продвигаются. Publication/cleanup holds,
immutable результаты, retained artifacts и recovery evidence не являются
расходным материалом для отзыва identity.

Это продолжение первого защитного среза, а не подтверждение завершения
source lifecycle или пунктов 2/7 общего плана.
Ревизия `686028c125f055382c48412c2218430908c2271f`
прошла итоговый обычный commit-hook с полным `task verify` и отправлена в
`go-development`; [CI 42360466600](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42360466600)
полностью завершён: verify, Docker Compose smoke, Linux arm64, macOS x64/arm64,
Windows x64 и публикация контейнера — success.

- Baseline 02 содержит необратимый `invalidated_at`: marker и detach фиксируются
  атомарно, включая active analysis holds; новое attachment/hold/execution/identity
  после отзыва запрещено. Существующие ownership и execution rows сохраняются.
- Analysis revocation проверяет operation/job/step attempt, lease, hold и immutable
  execution tuple. Live replay, admission, artifact reuse и linking старого work
  не восстанавливают identity. Retirement сохраняет tombstone и step history.
- Scan загружает identity targets один раз и фиксирует каждый подтверждённый
  mismatch отдельной транзакцией до буферизации. Неизменённые файлы не создают
  таких транзакций. Другая configured namespace не считается доказательством
  изменения прежнего inventory source.
- Settlement/recovery завершают invalidated delivery как `failed/source_changed`,
  очищают только analysis execution/borrower fields и analysis/tool holds;
  успешные selections/input/attempt и publication/cleanup authority остаются.

## Промежуточное ревью

Проверены границы live consumers: grouping сохраняет inventory member без
готовых результатов, а publication admission и выбор кандидатов явно исключают
invalidated work. Полный association witness и исторические readers не
фильтруются. Отдельный review scan-сервиса не выявил ошибок в порядке observation
до буферизации и возврата ошибки обхода.

Review worker выявил отсутствие повторной проверки исходного source при
переиспользовании retained staged artifact: проверка только копии недостаточна
для замеченного изменения исходного файла. Callback исправлен; существующая
unit fixture расширена изменением source после подготовки. Повторное review
подтвердило исправление и отсутствие найденных ошибок drainage: sibling runners
дожидаются завершения, input закрывается до завершения группы, active groups —
до settlement. На момент этого промежуточного review полный gate ещё не
запускался; итог его последующих запусков приведён ниже.

Интеграционная сверка выявила две дополнительные ошибки, не замеченные первым
review persistence: current-only reader скрывал marker queued work до settlement,
а retirement invalidated work мог удалять успешные step selections. Эти пути
исправлены вместе с точными live fences. Worker recovery также принимает
terminal `source_changed`, уведомляет UI и будит pending admission после
освобождения старых holds; обычная stale-root recovery сохраняет прежний no-wake.

Критическая SQL-проверка добавлена одним сценарием в существующую общую БД
artifact-тестов: реальный staged artifact с execution FK, исходная variant-связь,
invalidation, late SHA result, settlement и сохранение retained authority.
Новые отдельные БД для этих утверждений не создаются. Финальное независимое
review интеграционных исправлений не нашло дополнительных ошибок. При добавлении
сценарий ещё не запускался. Первый обычный hook остановился на Go typecheck: worker
обращался к `Work` у immutable execution registry record вместо work/step
execution snapshot. Обращение исправлено; полный hook повторяется без пропуска
проверок. Повторный hook выявил оставшийся неиспользуемый helper прежней
проверки identity; он удалён. Дополнительно закрыт early-copy-error путь:
неуспешное копирование повторно stat-ит source только без cancellation и
добавляет changed sentinel лишь при подтверждённом size/mtime mismatch.
Ошибка stat/unreadability сама по себе marker не создаёт. Изменение проверено
независимым review. Следующий полный hook прошёл lint и дошёл до Go-тестов,
но выявил регрессии streaming apply независимых шагов и очистки associations
после последующего исчезновения ранее invalidated location, а также прежние
fixture-ожидания удаления immutable identity/tombstone и неполные live retry
fixtures. Эти отказы не объявляются инфраструктурными. Streaming apply восстановлен,
для действительно исчезнувшей location собираются также исторические variant IDs;
изменённый, но присутствующий файл этим missing-path pruning не затрагивается.
Retry fixtures явно задают live attachment. Разрешённый диагностический прогон
упавших jobs/service/persistence-сценариев и критического artifact-сценария прошёл;
после исправлений итоговый полный hook прошёл в `686028c1`.

Дополнительная сверка native input validation закрыла replacement-путь:
при повторном открытии live leaf подтверждённое отличие size/mtime также
возвращает changed sentinel, даже если прежний pinned handle ещё читает старый
файл. Сам по себе namespace/inode mismatch без отличия size/mtime не становится
доказательством изменения identity. Независимое review не нашло ошибки этого
разграничения.
