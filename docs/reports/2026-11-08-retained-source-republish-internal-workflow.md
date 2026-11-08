# Internal same-path source republish workflow

База `2b38b827` прошла полный hook и всю CI 42487466240, включая контейнер.

Добавлен внутренний retain-selection путь: service готовит frozen assets вне
транзакции, typed persistence admission проверяет authoritative snapshot в
SERIALIZABLE transaction, затем существующий механизм атомарно создаёт River job,
operation, attempt, namespace reservations и holds. Поздние blanket table locks
не используются. Generic v4 admission остаётся закрыт без private typed hook.

Ограничения среза: уже выбранный compatible association, известная baseline source
authority, тот же CURRENT путь и exact active CURRENT namespace claim/reservation.
Нет смены selected association или authority mode. Ordinary при полном точном
совпадении не enqueue; forced не подменяет выбор manual authority. Все production
HTTP/UI и reconciliation producers остаются прежними, read-only CURRENT preview
не становится публичным подтверждением.

Executor явно готовит новый candidate из held selected source через private v4
preparation, проверяет исходный CURRENT file identity перед same-path replace,
передаёт optional result receipt и использует guarded freshness/CAS settlement.
SupportsExecution остаётся false: разрешение предоставляется специальным путём,
а не generic first-publication fallthrough. Changed-path и recovery adoption v4
не включены.

Interrupted v4 delivery durable блокируется до замены прежней executing claim:
claim recovery_required и failed operation со safe diagnostic; unsupported
attempt phase не вводится. Retry учитывает durable operation stage даже после
return. Root deletion после terminal exact River job может безопасно вернуть
ownership и архивировать original claim и terminal successor evidence отдельно,
сохраняя CURRENT/output. Обычный known-error/quiescent v4 не попадает в этот fence.

LRCLIB enabled включён в coherent planning-settings snapshot с установленным
default true; callback не может сравнивать два неявных false. В SQL callback нет
network/converter/native probes или pooled Registry reads.

Review помогло исправить imports/helper collision, initial-only ordinary blocker,
неверный namespace успешной fixture, candidate-generation assertions, recovery
retry bypass/terminal successor/root release и LRCLIB snapshot. Минимальные
регрессии используют существующие source/tool/CURRENT fixtures, real River insert
rollback, native same-path execution/redelivery и durable interruption cleanup.
Concurrency metadata-race regression отдельно не добавлена; isolation/locking
проверены статически. Полный hook и CI этого среза ожидаются.

Первый gate выявил ошибки компиляции в новых admission helpers; после исправления
focused диагностики подтвердили typed admission/real River rollback, interrupted
root cleanup и native v4 workflow. Native fixture задаёт реальную saved lyrics
authority и передаёт optional writer, а не подменяет production проверки.
Ordinary no-op дополнительно требует доказательства output transform: пока только
copy и точное совпадение сохранённых audio facts. Transcode/неполные факты не дают
ложного unchanged; bitrate, включающий container overhead, может дать консервативный
false negative. Это ограничение внутреннего среза, не public readiness.

Пункт 3 и общий план остаются открыты: public confirm, explicit source selection,
changed-path/recovery parity и оставшиеся интерфейсные пункты не завершены.
