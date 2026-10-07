# Независимое ревью плана 08 — 2026-10-08

**Итог: COMPLETE рекомендован в утверждённом scope.** Первоначальные pending
выводы ниже уточнены финальной независимой reconciliation в конце отчёта.

## Ревизия и результат

Независимый reviewer проверил итоговый код `a70ac2b`, включая C07 `7d481dc`,
и сопоставил его с просмотренным рабочим деревом. **Статических blockers C01–C08
не найдено.** Это ещё не итоговая runtime/browser-приёмка.

Reviewer не изменял файлы и не запускал gate. Оба итоговых коммита прошли
обязательный pre-commit `task verify` по свидетельству исполнителя; независимый
повтор gate здесь не заявляется.

## Сопоставление критериев

| Критерий | Независимая проверка |
| --- | --- |
| C01 | `SettingsScreen.tsx`: редактирование инвалидирует token, ответы preflight проверяют input/request/session revisions; submit повторно проверяет план и root. Summary использует захваченные входы. RTL покрывает true→false, возврат пути, поздние ответы и смену source root. Blockers нет. |
| C02 | Native `showModal`, локальные alert/focus refs, Escape/cancel, возврат фокуса и session guards проверены. Поздний accepted start сохраняет operation, не закрывая новый dialog. RTL покрывает отказ/retry и поздние ответы. Статических blockers нет; native поведение требует браузера. |
| C03 | `useSectionDraft`, revision acknowledgement, защита pending save, сериализация reads и текст dirty проверены. Другие saves/refresh сохраняют drafts; mutation success отличается от failed readback. RTL покрывает dirty publication/MusicBrainz, edit-and-revert и explicit false SHA-256. Blockers нет. |
| C04 | All-settled initial load, запрет duplicate retry, error rendering и mount/load generations проверены. Частичная загрузка не выдаётся за готовый экран. RTL покрывает повторный отказ и unmount. Blockers нет. |
| C05 | `settings.css` и `AppShell.tsx` используют существующие light/dark tokens и `prefers-color-scheme`; Setup/Sources сохраняют отдельные shell branches. Структурные tests есть. Статических blockers нет; визуальная проверка отдельна. |
| C06 | Service → registry → persistence, `SetMany`, runtime update, move admission/retry/switch/rollback проверены. Root mutations берут exclusive tools-move gate до subordinate locks; installation paths — shared gate до completion/package/row locks. Roots/semantics и reservations проверяются в коротких транзакциях; filesystem probes вне них. Отказ атомарен, output не перезаписывается компенсацией. Lock-order blockers нет. |
| C07 | Admission/retry/completion/finalization, delivery fencing, pinned-root cleanup и ownership rollback проверены. Post-Setup download не меняет active ID автоматически. Failed history не резервирует initial slot навсегда. Legacy multiple-ready сохраняется без самовольного repair. Blockers нет. |
| C08 | README описывает `task stop`; `Taskfile.yml` подтверждает detached Compose run и stop той же конфигурации. Blockers нет. |

## Regression references

- C06: `backend/internal/persistence/tools_root_race_integration_test.go`:
  `TestToolsRootUpdateAndInstallEnqueueSerializeWithPostgreSQL`,
  `TestRuntimeRootsRespectActiveMoveAndKeepNonconflictingOutputUpdatesPostgreSQL`,
  `TestGenericSettingsWritesSerializeActiveSelectionsAndCanonicalizeRootsPostgreSQL`;
  `setup_manager_integration_test.go`:
  `TestCommitToolsRootMoveSwitchesSettingAndOperationAtomically`.
- C07: persistence `c07_admission_regression_integration_test.go`,
  `c07_delivery_regression_integration_test.go`,
  `c07_retry_regression_integration_test.go`,
  `c07_completion_race_integration_test.go`; API
  `c07_activation_gate_integration_test.go`.
- Workers: `install_worker_test.go` проверяет pinned-root cleanup, partial
  publication, lost commit response, unknown-file ownership и backup recovery.
- Frontend: `SettingsScreen.test.tsx`, `SetupTools.test.tsx`,
  `SetupManager.test.tsx`, `AppShell.test.tsx`.

## Ограничения

Наличие tests не означает независимый запуск reviewer. C06 tests покрывают оба
порядка admission/update, overlap, atomic refusal, неконфликтующий output,
direct root change и failed-move retry. Отдельный concurrent root-switch versus
output-update barrier test не найден; switch guards проверены статически.
Такой отдельный выполненный сценарий не заявляется.

Execution mutex process-local; вывод относится к утверждённому single-process
deployment. Синтетические fixtures не доказывают реальные downloads или всю
native-platform матрицу. Legacy repair требует решения владельца.

## Runtime/browser evidence

**Ожидается финальный прогон**, не PASS. Проверяемый immutable binary SHA-256:
`d91985ada3dcd5cc66ba38bc1d5f7e9b29bedb4970ba4f74c68d9064e264065e`.
После прогона необходима reconciliation с acceptance report: native modal,
реальный move без удаления старых files, dirty output, initial retry,
light/dark 1440/375 и Setup/Sources. До этого план остаётся IN PROGRESS.

### Независимая сверка выполненных свидетельств

Reviewer сопоставил acceptance addendum, runtime manifest, bootstrap/final-move
JSON, browser-actions и screenshots; SHA-256 бинарника подтверждён. Реальный API
move `f6fa5305-2b01-4b09-925b-f5a70d61655a` завершён успешно; JSON фиксирует
сохранение трёх старых executable files и совпадение target hashes. Последующий
реальный browser move подтверждён persisted operation
`f2edc76e-b420-4e8e-b329-3f371b1a658f` (`succeeded`, `switched`). Screenshot и
server settings совместно подтверждают сохранение несохранённого Output draft.

Сверка выявила ограничения provenance: embedded Go metadata этого бинарника
указывает `7d481dc`, `vcs.modified=true`, manifest содержит устаревшее имя DB.
Соответствие итоговому дереву требует дополнительного подтверждения. До этого
и supplemental проверки install-start refusal/полного Tab loop COMPLETE не
заявляется. Новых product blockers не обнаружено.

### Финальная независимая reconciliation

Reviewer проверил supplemental evidence для `a70ac2b`: runtime binary SHA-256
`0046901c2e5b2fda3389155ae3de731dde784769a2fc5ca1d1ef1b38ac3c6325`
и embedded revision совпадают с final-gate provenance. `vcs.modified=true`
раскрыт; tracked diff пуст, manifest содержит исправленную DB identity.
Просмотрен gate log с 195 passed frontend tests; reviewer gate не запускал,
exit 0 принят по свидетельству исполнителя.

Новый реальный browser move `55d0fcc9-59e0-433f-922d-4d8f00596e33`
независимо подтверждён API как `succeeded / switched`; driver/evidence фиксируют
true→false, исчезновение старого confirmation, новый preflight и сохранение
dirty Output draft. Screenshot показывает новый root и unsaved-publication.
Reviewer дополнительно проверил три executable files в прежнем root и равенство
SHA-256 новым копиям. Исправленный исторический ID предыдущего browser move:
`f2edc76e-b420-4e8e-b329-3f371b1a658f`.

Supplemental mocked browser подтверждает install-start refusal с alert focus
внутри native modal и successful retry. Forward Tab в Chrome на границе временно
даёт activeElement BODY, следующий Tab возвращается к modal controls; reverse
traversal остаётся на controls, dialog сохраняет `:modal`. Interactive-background
focus escape не обнаружен. Это native-navigation limitation, не blocker C02
и не основание вводить новый custom-focus-trap requirement.

Mocked HTTP/SSE — только UX evidence; downloads/Setup предыдущего артефакта не
выдаются за повтор на последнем бинарнике. Concurrency опирается на committed
tests и reported gate; вся platform matrix и WCAG certification не заявляются.
С учётом static review C01–C08 и supplemental reconciliation blockers не осталось:
**рекомендуется COMPLETE в утверждённом scope**.
