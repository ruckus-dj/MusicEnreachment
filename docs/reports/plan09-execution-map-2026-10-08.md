# План 09 — фактическая карта исполнения на 2026-10-08 (D01)

**Кодовая база:** `959c680` (HEAD при составлении карты). Это описание существующего
поведения, не приёмка будущего staged-контракта и не список дефектов. Нормативный
источник требований — [последние решения владельца](../plans/todo/09-staged-source-analysis-owner-decisions.md).
Публикация остаётся за пределами плана 09.

## Вывод

Текущая реализация — не staged pipeline: отдельный `scan_source` обходит дерево,
для каждого кандидата может вызвать общий подготовитель анализа и сохраняет
подготовленные данные в scan candidate; только затем scan применяет целое новое
поколение inventory. Повторные отдельные операции анализа уже работают с
нормализованными `source_analysis_work`/`source_analysis_step`, читают исходник
напрямую и используют delivery fencing. Отдельных per-file jobs, owned audio
copies и выбора режима root в поставленном контракте пока нет. Эти факты не
переопределяют одобренную владельцем будущую модель.

## Текущий путь scan → inventory и анализа

1. **Admission.** `service.SourceScanOperations.Start`
   (`backend/internal/service/source_scan_start.go`) валидирует root, Setup,
   platform и путь; сам каталог не обходит. `CreateSourceScanOperationAndEnqueue`
   (`backend/internal/persistence/source_scan_admission.go`) создаёт operation и
   River job атомарно, с проверкой/координацией root. Для уже существующего
   `scan_source` очередь передаёт ID operation.
2. **Traversal/candidates.** `service.SourceScan.Run`
   (`backend/internal/service/source_scan.go`) открывает root через `sourcefs`,
   получает наблюдения файлов, строит кандидаты и передаёт их пачками. В текущем
   коде `candidateFor` может вызвать общий preparer; это не enumeration-only.
   Кандидаты сохраняются под идентичностью delivery в
   `source_scan_candidate` (`persistence.storeSourceScanCandidates`,
   `backend/internal/persistence/source_candidates.go`). Ошибка обхода сбрасывает
   только попытку scan, а прежний inventory остаётся опубликованным.
3. **Publication/reconciliation.** `SourceInventoryRepository.ApplySourceScan`
   (`backend/internal/persistence/source_repository.go`) выполняет транзакцию,
   блокирует root, повторно проверяет delivery и применяет candidates к новому
   generation. `applySourceScanCandidates` обновляет видимые locations и
   pruning по завершённой выборке; удаление analysis work вместе с location
   проходит через `removeScanAnalysisWork`
   (`backend/internal/persistence/source_scan_publish_analysis.go`). Удаление
   location также чистит связанные fingerprint references с учётом других
   ссылок. Scan recovery в `RecoverInterruptedSourceScan` не публикует кандидаты
   незавершённого обхода.
4. **Pending analysis и отдельный retry.** `SourceAnalysisOperations` в
   `backend/internal/service/source_analysis_start.go` строит пакетные и
   single-step операции; `AdmitPending` в
   `backend/internal/service/source_analysis_pending.go` выбирает ожидающие
   шаги. `CreateNormalizedSourceAnalysisOperationAndEnqueue`
   (`backend/internal/persistence/source_analysis_admission.go`) сериализует
   admission против конкурентных мутаций, ставит step delivery fences и вставляет
   River job в той же транзакции. `RetryStep` и `RerunFingerprint` — явные
   операции; инструментальные selection сейчас закрепляются при подготовке
   операции.
5. **Worker и независимые результаты.** `SourceAnalysisWorker.runWorkGroup`
   (`backend/internal/jobs/source_analysis_worker.go`) получает execution tuples,
   независимо claim-ит steps с operation/attempt/job identity, готовит SHA прежде
   иных шагов, затем при необходимости готовит probe/fingerprint. Ошибка шага
   сохраняется отдельно, а sibling может завершиться успешно. Переходы
   `ApplySourceSHA256`, `ApplySourceProbe`, `ApplySourceFingerprint` и
   `FailSourceAnalysisStep` находятся в
   `backend/internal/persistence/source_analysis_steps.go`; `ClaimSourceAnalysisStep`
   проверяет текущую operation и exact delivery fence.
6. **Terminal settlement и recovery.** `SettleNormalizedSourceAnalysisOperation`
   (`backend/internal/persistence/source_analysis_operation.go`) завершает
   operation, освобождает durable holds и убирает соответствующие fences в
   согласованном DB-переходе. Startup `ReconcileInterruptedOperations`
   и `recoverInterruptedSourceAnalysis` (`backend/internal/jobs/reconcile.go`)
   находят осиротевшие delivery; `RecoverInterruptedSourceAnalysis`
   (`backend/internal/persistence/source_analysis_recovery.go`) повторно
   валидирует захваченные attempt/job после lock acquisition и атомарно
   завершает recovery, не декодируя потенциально некорректный старый snapshot.

## Source reads и место для одного prepared input

| Работа | Текущий код / чтение | Важное отличие от будущего контракта |
| --- | --- | --- |
| Inventory stat/enumeration | `walkSourceRoot`, `candidateFor`, `SourceScan.Run` в `backend/internal/service/source_scan.go`; traversal через `sourcefs` | Scan сейчас не только перечисляет и stat-ит: кандидат может подготовить анализ. В новом контракте scan только перечисляет/stat-ит. |
| SHA-256 | `SourceAnalysisWorker.prepareWorkSteps` и source preparer в `backend/internal/jobs/source_analysis_worker.go` / `backend/internal/service/source_analysis_preparer.go`; применение через `ApplySourceSHA256` | Текущий input открывается от source; устойчивой owned staged-копии нет. SHA отключённый/включённый режим и cache policy существуют как текущие runtime inputs. |
| ffprobe | общий `SourceAnalysisPreparing` pipeline, вызываемый scan candidate и analysis worker; результат применяет `ApplySourceProbe` | Повторное сканирование/подготовка может приводить к probe вне целевого per-file lifecycle. Целевой integration point — общий preparer, которому передаётся уже подготовленный input, не второй analyzer и не повторный probe. |
| fpcalc | тот же shared preparer; `ApplySourceFingerprint`; provenance/версия сохраняются отдельно | Fingerprint сам по себе не identity; сохранение успеха независимо от probe уже поддерживается. |
| Retry/rerun | `RetryStep`/`RerunFingerprint` (`backend/internal/service/source_analysis_start.go`) плюс worker preparation | Сейчас повторная работа читает source. Целевой retry должен использовать пригодную durable copy либо создать её снова, не теряя успешные соседние шаги. |

Ключевой факт для интеграции: повторно использовать `SourceAnalysisPreparing` и
step-specific `Apply...` путь, отделив один подготовленный file input от его
потребителей. Не переносить копирование/probe/hash в scan.

## Durable inputs, fences, locks и filesystem/DB границы

- **Operation snapshot сегодня.** `SourceAnalysisOperationSnapshot`
  (`backend/internal/persistence/source_analysis_snapshot.go`) включает mode,
  `WorkIDs`, rerun/SHA/cache intent, tool pins и selected steps; комментарий
  прямо требует использовать сохранённые selection, не перечитывая active tools.
  `Operation.InputSnapshot` расположен в `backend/internal/persistence/setup_manager.go`.
  `SourceAnalysisStep.InputSnapshot` (`source_analysis_steps.go`) отдельно
  сохраняет нормализованный step intent. Это противоположно одобренному
  минимальному job contract: будущая job передаёт operation ID/минимальные IDs и
  явный intent, а settings/config читаются актуальными при start/retry/execute;
  `input_snapshot` полной конфигурации не персистится и не копируется.
- **Текущие fencing/holds.** Step fence — `(execution_operation_id,
  execution_operation_attempt, execution_job_id)`; его claim проверяет в
  persistence. Durable source-media/fingerprint/tool read holds связываются с
  operation. `AcquireSourceScanToolHold` и release в
  `backend/internal/persistence/source_scan_analysis_inputs.go` показывают
  аналогичную попытку/job fence для удержаний инструмента. Analysis admission и
  settlement управляют своими holds в persistence. Это DB ownership; текущая
  модель не является ownership временных audio-файлов.
- **Текущая координация.** Analysis admission в
  `source_analysis_admission.go` использует shared tools-root gate/package
  selections, root/location/work/installation/operation/step locks. Совместная
  последовательность указана в комментарии admission: tools gate/package,
  root, location, work, installation, operation, step. Root mutation uses
  `activeSourceRootMutationExists`/`sourceRootActiveOperationError` там же.
  `ApplySourceScan` блокирует root и проверяет operation delivery в транзакции.
  Эти механизмы — ориентир, а не готовая блокировка output reset.
- **Filesystem вне транзакций.** `SourceScan.Run` открывает/обходит source и
  managed tools, а worker/preparer открывает source и запускает инструменты вне
  DB transaction. Транзакции вокруг admission/apply/step publication не делают
  filesystem writes атомарными с PostgreSQL. Нынешний scan candidate — DB
  staging состояния, не audio-copy. Нынешняя recovery не имеет staging-copy
  artifact для удаления.
- **Restart.** Утрата River delivery обнаруживается reconciler-ом; per-step
  analysis становится failed/retryable, holds/fences освобождаются. Для нового
  staged контрактa этого недостаточно: явная DB ownership/path/identity и
  fenced cleanup должны покрыть crash между созданием файла и его публикацией.

## Релевантные UI/API и разночтения

- Sources UI: `SourceRootsScreen`, `SourceInspectorScreen`, `SourceScanControl`
  и `SourceAnalysisSteps` в `frontend/src/features/sources/`; scan и analysis
  доступны существующими operation/API контрактами. `SourceAnalysisSteps`
  показывает per-step state/retry и fingerprint rerun. Действия в UI не дают
  режима root или staged-copy lifecycle.
- Backend routes/DTO: `backend/internal/api/sources.go`,
  `backend/internal/api/source_analysis.go`, операции и settings endpoints;
  generated client — Orval output под `frontend/src/api/generated/`.
- Runtime settings сохраняются через settings registry/database; Compose output
  сейчас явно bind-mounted в `/var/lib/melotrove/output`. README описывает
  read-only source mounts. Никакого work/staging mount или нового env var сейчас
  нет.
- Нормативные divergence (не новые замечания кода): per-root mode отсутствует;
  scan делает больше enumeration; нет per-file processing jobs; snapshot и
  selected tool pins шире одобренных; audio copy отсутствует; обработка не
  независима от scan generation; нет output reset, output-managed областей или
  bulk cleanup workflow. Это описывает текущий срез относительно будущей цели,
  а не дефекты, исправляемые без следующего одобрения.

## Результат D01 и границы

Карта фиксирует admission → scan/candidate → inventory publication → pending / retry
analysis → settlement/recovery и точные точки source input, holds/fences и
filesystem/DB разделения. D01 выполнен документально; никакое поведение приложения
этим не изменено. Детальный proposed UI/lifecycle/DB/API/deployment контракт и
последовательная реализация вынесены в отдельные документы плана 09. Открытый
вопрос о представлении нескольких версий одного fingerprint result против одной
текущей SHA-identity не решён этой картой.
