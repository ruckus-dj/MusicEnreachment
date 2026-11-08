# Source republish: interrupted same-path recovery

База `a94531dc` прошла полный hook и всю CI 42509889462, включая контейнер.

Протокол установки не изменён: source/remux/tags пишутся только в private candidate,
после validation выполняется atomic same-filesystem MOVE и затем DB commit.
Recovery не принимает private stages. Final, совпадающий с frozen OLD CURRENT,
не становится новой publication: guarded reset разрешает полную подготовку из
source. Изменившийся final инспектируется через pinned no-follow handles и private
read-only snapshot, проверяется по frozen v4 inputs и повторно сверяется перед
обычным result commit. Необъяснимый drift/missing/невалидный target не получает
разрешение на произвольную перезапись. Побайтово одинаковый OLD target без exact
identity не принимается за новый результат.

Миграция `20261108000002` добавляет bounded attempt resume_mode, а не журнал
подготовленного output или success stamp. Cross-boot v4 получает successor claim
с полным guard; inspection mode переживает returns/retries до успешного settlement
или guarded возврата к подготовке. Retry требует terminal proof exact claimed
River attempt. Старые parked recovery_required rows сохраняют cleanup path.

Миграция `20261108000003` расширяет только v2 cover projection на наблюдаемое
отсутствие после прерывания. Interruption-only result receipts принимаются только
при persisted inspection mode. Lyrics summary сообщает unknown acquisition и
actual digest/absence, не выдумывает source/LRCLIB history или rebuild. Manual value
при отсутствии остаётся pending; explicit-empty и exact manual value подтверждаются
по фактическому содержимому. Обе down migrations отказывают при retained recovery
authority/новых projections. V1 cover acceptance не расширяется.

Область гарантии — managed output actor и существующий atomic-install protocol.
ffprobe/tag readback и full-file hashing не являются обещанием полного декодирования
всего audio stream; отдельный произвольный внешний writer этим протоколом не
авторизуется. Ранее заданный владельцу вопрос был преждевременным: сначала следовало
разобрать эти технические границы; новых продуктовых правил срез не вводит.

Review помогло исправить service/persistence observation mapping, v1/v2 projection
расхождение, недостаточно точный terminal retry witness и неиспользуемые fixture
декларации. Native grouped scenario проверяет before-MOVE повторную подготовку,
after-MOVE DB rollback с диагностикой, source-gone recovery и completed redelivery.
Новые tests используют существующие fixtures, без нового format/asset cross-product.
Полный hook и CI этого среза ожидаются. Public confirmation и общий план не закрыты.

Первый gate выявил Bun zero-value insert для resume_mode, ошибки tagged fixtures
и staticcheck switch warnings. SQL CHECK не ослаблен: модель отправляет DEFAULT,
а admission явно задаёт prepare. Диагностика native timeout показала ошибку модели
рестарта: boot UUID менялся при прежнем coordinator, чьи locks намеренно сохранялись
при unknown quiescence. Fixture теперь создаёт новый executor/coordinator, как
реальный новый процесс, сохраняя shared writer counter для assertions. Таймауты
не повышались и safety locks в production не освобождаются произвольно.
Focused native recovery, sticky claim/legacy cleanup и migration rollback tests
после исправлений прошли. Raw metadata authority fixture также перечитана из БД,
а не подменена отсутствием. Повторный полный gate ещё ожидается.
