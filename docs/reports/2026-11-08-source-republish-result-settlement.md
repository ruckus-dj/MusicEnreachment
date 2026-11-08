# Source republish: atomic exact-result settlement

База `1acfe61e` прошла полный hook и всю CI 42481076121, включая контейнер.
CI `1f73a6fc` (42479192941) после повторного acquire PostgreSQL также успешна.

Добавлена закрытая v4 ветка CommitPublicationResult для exact-success subset.
Проверяются полный CURRENT/source-authority witness, raw lyrics authority,
selected cover authority, фактические tags/audio/assets и installed ownership.
При успехе source authority получает выбранную identity candidate и новую revision,
сохраняя baseline mode. CAS failure после writes возвращает ошибку для rollback.

Старые lyrics/cover summaries и applied receipts не наследуются. Новые projections
строятся из frozen evidence и actual outcomes; original/derivative SHA различаются.
Ручной explicit-empty/value получает собственный applied receipt. Cover selection
не меняется; pending не применяется. Implicit absent cover сериализуется через
материализацию default authority row и блокировку.

Этот subset явно отклоняет optional failures, selected-value без embedding и
непредставимые manual states, пока diagnostic bridges не реализованы. Private
preparation сохраняет прежнюю optional-семантику, но production enqueue/исполнение
v4 всё ещё закрыты: SupportsExecution false, generic admission запрещён, executor
не подключён. Это не готовая перепубликация и не закрытие пункта 3.

Review выявило и помогло исправить preservation bit depth/layout при copy,
AAC lossy classification, locking implicit absent cover и неправильные ожидаемые
counts в rollback fixture. Дополнительно сохранено различие nil/empty tag values.
Один grouped PostgreSQL test использует одну БД: сначала success baseline,
затем late injected SQL failure с проверкой его diagnostic и полного rollback.
Полный hook/CI нового среза ожидаются.
