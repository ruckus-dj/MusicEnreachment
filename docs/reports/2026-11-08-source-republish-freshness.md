# Source republish: guarded CURRENT freshness

База `1f73a6fc`. Общая точная проверка database-backed CURRENT witness выделена
из существующего provider-tag пути без изменения его семантики: epoch/generation,
путь, ownership identity, audio facts, tags и asset outcomes.

Guarded preparation reader и CheckPublicationResultCurrent получили отдельную
v4 ветку: полный frozen CURRENT и raw source authority сравниваются в существующей
транзакции с блокировками. Baseline identity не обязана совпадать с выбранным
источником candidate. Present authority обязательна в этом ограниченном пути;
revision, mode, position, epoch/generation и source identity проверяются точно.

Это не активация исполнения. Generic admission, result commit и executor v4
по-прежнему закрыты; SupportsExecution false. Нет нового enqueue producer.
Физическая проверка файла остаётся обязанностью существующего native delivery
пути; база не хранит отдельный artwork inventory для такого сравнения.

Review выявило и помогло исправить enclosing eligibility gate, который отказывал
v4 до новой ветки, и неатомарную смену intent в DB fixture. Один PostgreSQL тест
использует существующую claimed CURRENT fixture и её БД: сначала доказывает
успешное чтение обоими guarded readers, затем проверяет отказ после смены revision.
Это не проверка admission/source inventory/tools или end-to-end исполнения.
Полный hook и CI нового среза ожидаются.

CI AUDIO-среза `e6e7a34c` (42478104463) полностью успешна после rerun Linux
Task download failure. Для `1f73a6fc` (42479192941) Windows/macOS/Compose успешны;
verify и Linux arm64 не смогли acquire PostgreSQL до deadline. Failed jobs
перезапущены без изменения gates или таймаутов; полный результат ещё ожидается.
