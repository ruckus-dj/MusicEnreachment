# Чистый повторный прогон на пересобранном image

Дата: 14 августа 2026 г.  
Среда: disposable `test_stand`, PostgreSQL и in-process worker.  
Image precondition: runtime подтверждённо содержит `reassociation`,
`reassociation_recovery` и `_stored_match_tags`.

## Методика

1. `test_stand/scripts/reset-library.sh` очистил только импортированные таблицы
   и managed output; source trees остались read-only.
2. Через `POST /api/reconciliation/scan` создан job
   `reconciliation-scan-c16d3ab7e52f4dfea871df9567e16225`.
3. Scan создал 1 093 source observations; worker queue дошла до терминальных
   состояний.
4. Итог снят через `GET /api/library/records`, `/api/workers/queue` и
   read-only PostgreSQL cohort queries.

## Итог

| Показатель | Rebuilt image | Clean run 04 |
| --- | ---: | ---: |
| Library records | 1 370 | 1 443 |
| Manual Actions (`needs_review`) | **303** | 796 |
| Analysis errors | 254 | 14 |
| Активные/ожидающие worker jobs | 0 | 0 |

Manual Actions уменьшились на 493 строки (61,9 %). Сравнение не является
изолированным benchmark: snapshots источников и итоговая consolidation record
count различаются. Но ключевой workflow cohort снизился независимо от этого.

## Latest-event cohort ручной проверки

| Event | Rebuilt image | Clean run 04 |
| --- | ---: | ---: |
| `analysis_ready_for_review` | 150 | 283 |
| `stored_candidates_auto_selected` | 126 | 257 |
| `selection_refresh_no_final_revision` | **27** | 256 |
| **Всего** | **303** | **796** |

Падение `selection_refresh_no_final_revision` на 229 строк подтверждает
reassociation recovery. Stored provider candidate path теперь создаёт
append-only `analyzed` и provider-derived `final` revisions, затем ставит
`final_publish`; он больше не ограничивается записью MBID.

## Provider reliability caveat

251 `musicbrainz_analysis` jobs завершились `blocked_infrastructure`; ещё три
sources quarantined на filesystem scan. Поэтому UI показывает 254 «Ошибки
анализа» наряду с 303 строками «Нужна проверка». Это не безопасные кандидаты,
которые можно auto-select: у них нет терминального подтверждённого provider
outcome.

Завершённые provider attempts:

| Provider | Outcome | Количество |
| --- | --- | ---: |
| AcoustID | `acoustidmatch` | 1 013 |
| AcoustID | `nomatch` | 77 |
| MusicBrainz | `musicbrainzmatch` | 418 |
| MusicBrainz | `ambiguous` | 418 |
| MusicBrainz | `nomatch` | 4 |

Следующее улучшение должно отдельно диагностировать и стабилизировать
MusicBrainz infrastructure cohort; его нельзя маскировать автоматическим
выбором release.
