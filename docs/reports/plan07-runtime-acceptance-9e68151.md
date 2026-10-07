# Этап 07: приёмка runtime 9e68151

Дата: 2026-10-07. Проверяемый runtime commit:
`9e681510e2a63677cc12a20d6bbc5f678251818c`.

Бинарник собран после успешного `task verify` из чистого дерева:
`vcs.modified=false`, SHA-256
`f0ef778fd7481ad164b68e1ef168c30fe391a5810444986e6a81bc227c1e5286`.
Стенд — отдельная принадлежащая этому прогону PostgreSQL БД и native app;
managed ffmpeg 9.0.2, fpcalc 1.6.0 и 1.6.1. Для временного стенда владелец
одобрил publication_format=source; defaults приложения не менялись.
fpcalc использует встроенный default 120 секунд, без аргумента длины.

## Реальные сценарии

Все шесть сценариев завершились успешно на чистом бинарнике:

- initial: независимые результаты SHA/probe/fingerprint, ошибки broken.wav,
  реальный video-only no_audio с успешным SHA;
- unchanged: сохранение результатов, без повторения ошибочного fingerprint;
- duplicate: полный fingerprint provenance совпал с SQL first-winner для
  точных SHA/version; selected probe совпал с SQL probe-cache winner;
  собственный результат исходной location остался неизменным;
- changed: изменились SHA и выбранный variant;
- toggle: отключение/включение SHA не изменило сохранённые результаты,
  unchanged location не получила backfill; изменённый файл получил SHA;
- lazy: смена активной версии не пересчитала результаты; explicit rerun
  обновил только fingerprint выбранного файла.

Source root этого прогона: `54a2be59-f29a-4db6-be69-ef92d9b7d0fe`.

## Retry, история и crash/restart

Exact probe retry на намеренно повреждённом файле сохранил успешный SHA
и fingerprint-сосед, а probe завершился ожидаемой независимой ошибкой.
Retained probe input snapshot сравнивался целиком, включая installation ID
и полный ffprobe banner.

На чистом бинарнике приложение получило SIGKILL после admission probe retry.
До restart проверено сохранённое queued/running состояние. Interrupted
operation `9b68594b-b58a-4c6e-a295-ad10fbc44490` стала failed/recovered;
unfinished probe доставлен successor operation
`1cdc4022-5a28-4732-b633-eaa7153e62ef`, завершившейся ошибкой broken fixture.
SHA/fingerprint-соседи и retained probe inputs остались неизменными.

Удаление successor через публичный API вернуло 204, последующее чтение — 404.
Retained probe inputs до и после удаления истории были точно равны.

## Артефакты и исправления стенда

Локальные исходные артефакты находятся в принадлежащем прогону каталоге
`plan07-acceptance-2d0eb7c6-0eb7-437e-9afc-b2e958275a02`:

- `clean-9e68151-complete-run/manifest.json` и `http.jsonl`;
- `clean-9e68151-complete-run/history-retained-clean.json`;
- `final-commit-run/crash-recovery-clean-9e68151.json`;
- `server-final-clean.log`.

Ранние неуспешные прогоны не засчитывались. Исправлены ошибки harness:
отсутствующий broken fixture, повторно выбранная та же частота changed fixture,
жёстко заданная версия и ошибочное предположение, что результат original
обязательно является cache winner. Полные assertions provenance сохранены.
Первоначальное ожидание одного прироста probe attempt при restart было неверным:
recovery создала successor delivery; обе операции сохранены в evidence.

Этот отчёт подтверждает перечисленную runtime-приёмку, но сам по себе
не является отметкой выполнения всего checklist этапа.
