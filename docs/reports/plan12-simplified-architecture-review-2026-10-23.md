# План 12: независимое архитектурное review упрощённого контракта — 2026-10-23

## Результат

**APPROVEWITHCONDITIONS** — архитектурное review одобряет упрощённый
контракт публикации при соблюдении границ и проверок ниже. Это review документа и
архитектурного предложения, не приёмка реализации: код просмотрен только для
архитектурных привязок, реализация и runtime не проверялись, `task verify`,
focused tests и native CI не запускались.

## Одобренные границы

- Durable River job несёт только operation ID. Worker заново читает актуальные
  входы, целиком готовит кандидат приватно на той же файловой системе рядом с
  фактическим destination, валидирует/readback-проверяет и выполняет native
  atomic replace. После этого одна DB-транзакция записывает actual projection и
  успешную operation, освобождает holds и потребляет attempt.
- Сбой до DB commit означает повтор всей подготовки и допускает замену target,
  который уже был заменён предыдущей доставкой. Exactly-once filesystem effect,
  journal/ACK, backup/rollback, resume и boot recovery не входят в контракт.
  Crash после DB commit до завершения River job должен завершаться redelivery без
  повторной файловой работы.
- Source-free rewrite читает текущий destination и сохраняет audio и artwork;
  lyrics update/removal не должны вызывать lossy re-encode. Один общий coordinator
  сериализует publication с конкурентными сохранением, reset и delete, без SQL
  transaction вокруг ffmpeg.
- Source cleanup начинается только после подтверждённого DB-результата и только
  по актуальному opt-in с защитой consumers. При выключенном opt-in cleanup не
  стартует. При смене пути или расширения артефакта в пределах текущего output
  новый DB result и durable cleanup intent/job при необходимости создаются в одной
  транзакции; удаление прежнего managed файла выполняется после commit отдельно,
  со свежими проверками и без post-commit enqueue gap; ошибка — обычный warning.
  Смена или сброс output root сюда не входят, старые физические файлы не удаляются.
- Отмена и retry exhaustion приводят к видимой ошибке и ручному retry, не к
  вечному running или раннему cleanup. Ожидание основного процесса и собственных
  callbacks достаточно; process-tree proof не требуется.
- Atomic replace ограничен поддерживаемой native same-FS операцией; delete-copy
  fallback запрещён. Обещание универсальной power-loss durability отсутствует,
  directory sync не является условием atomicity. Байтовая детерминированность
  целого файла между полными повторами не требуется; повторная работа и
  кратковременное расхождение file/DB до commit допустимы.
- Внутренняя совместимость dev-контрактов V1/V2/V3 не требуется. Независимые
  решения об инвентаре и source cleanup остаются в силе.

## Условия реализации и верификации

Условия — архитектурные границы одобренного контракта, а не дополнительные
продуктовые решения: доказать сериализацию/ownership target и freshness manual
selection; покрыть crash после replace до DB commit и после DB commit до River
completion; проверить lyrics add/update/removal с сохранением audio/artwork;
проверить reset/save/delete interleavings, terminal cancellation/retry failure,
cleanup выключенный opt-in и redelivery без file work. Подтвердить native
atomic-replace поведение на поддерживаемой platform matrix и отдельно проверить
post-commit cross-path cleanup warning. Локальную полноту реализации и фактические
результаты должен подтвердить обычный проектный gate и CI; этот отчёт их не
предвосхищает.

## Ограничения review

Ревизия кода и её runtime-поведение не оценивались. Review подтверждает только
архитектурную согласованность контракта в границах этого документа; оно не
утверждает, что реализация уже соответствует ему или что план 12 завершён.
