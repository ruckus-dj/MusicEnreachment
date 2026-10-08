# Планы

- `todo/` — согласованные, достаточно декомпозированные этапы: готовые к
  реализации или уже выполняемые.
- `to-decompose/` — будущие области и сохранённые решения, которые нельзя брать
  в реализацию без повторной проработки и разбиения на короткие понятные этапы.
- `done/` — завершённые планы, прошедшие независимую приёмку.

## Следующий этап

Текущий этап — [план 09: проработка staged source analysis](todo/09-staged-source-analysis-design.md),
дополненный [решениями владельца от 2026-10-08](todo/09-staged-source-analysis-owner-decisions.md).
Статус: D01 factual map на `959c680`; D02 scenarios и D03–D05 технические
предложения собраны в контракте, D06 — в implementation proposal. Cache policy,
reset scope и root lifecycle заданы решениями владельца; физическая схема/API и
reset recovery остаются техническими предложениями. Независимое ревью выполнено,
но финальное явное owner approval контракта и плана ожидается. UI screenshot
сопоставление текстовое: полноценный visual/browser review не проводился и dark
Sources/Settings screenshots в проверенном каталоге не обнаружены. Приложение не
реализовано; реализация не начата.

- [Контракт D03–D05](todo/09-staged-source-analysis-contract.md) — UI-flow,
  lifecycle, D02 scenario matrix, DB/API/deployment proposal и технические детали
  для review; это не owner acceptance и не implementation evidence.
- [Implementation proposal](to-decompose/09-staged-source-analysis-implementation-proposal.md)
  — последовательные маленькие commits, зависимости, reset interlock/journal,
  bulk cleanup и verification; ожидает explicit owner approval до реализации.
- [D01 execution map](../reports/plan09-execution-map-2026-10-08.md) — фактический
  code map, не требования и не свидетельство поставки staged mode.

## Завершено

- [План 08: согласование Settings и Setup с дизайном](done/08-settings-and-setup-design-corrections.md)
  — COMPLETE (2026-10-08): C01–C08, полный `task verify`, реальные Setup/move,
  browser UX и [независимая приёмка](../reports/plan08-independent-review-2026-10-08.md).
  Binary provenance и платформенные/browser ограничения раскрыты в отчётах.

- [План 07: автоматический поэтапный анализ](done/07-automatic-source-analysis.md)
  — COMPLETE (2026-10-07): полный `task verify`, реальный PostgreSQL/River прогон
  и [независимая приёмка](../reports/plan07-independent-review-2026-10-07.md).
  Ограничения платформ и runtime evidence указаны в отчётах.
- `done/06-done-plans-audit-corrections.md` — A01–A09 исправлены; независимый
  `task verify` завершился с кодом 0, реальный ручной сценарий и полный native
  CI `37153819171` прошли. Ограничения UNC/SMB и доказательств указаны в
  независимом [отчёте приёмки](../reports/plan06-independent-review-2026-10-04.md).
- `done/03-source-inventory-first-slice.md` — все 10 этапов реализованы;
  независимый проверяющий повторил `task verify` с кодом 0 и сопоставил девять
  критериев готовности с кодом, миграциями, тестами и живым UI.
