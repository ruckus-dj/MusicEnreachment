# План 12 — итоговая сверка 2026-10-26

## Проверяемая ревизия

`ba290402051462f4dd98832d0b85a07d44f06434`. Полная GitHub CI
[40284639966](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/40284639966)
завершилась успешно. Предшествующее исправление AAC/M4A validator `5d007d2`
также прошло полную CI
[40283218319](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/40283218319).
Повторный локальный `task verify` завершился успешно: generate, lint,
backend integration, 349 frontend tests, 17 tools tests и build. Проверенный
server SHA-256:
`e933eeccc28fd3ccf5c96ac2e627f001e6605754d1fe99a47e23f5654bb21915`.
Go VCS revision соответствует `ba29040`; `vcs.modified=true` записан как есть:
в дереве имеются documentation edits и посторонние untracked files.
`git diff --exit-code HEAD -- backend frontend tools Taskfile.yml` успешен.
Результаты дополнительных runtime-прогонов приведены ниже. Этот отчёт не
объявляет план завершённым.

## Сверка ранее обозначенных пробелов

- `TestPublicationSimpleDeliveryRetriesSettlementAndAcknowledgesRedeliveryPostgreSQL`
  (`service/publication_simple_retry_integration_test.go`) проверяет установленный
  файл после ошибки DB settlement, повторную полную preparation и completed
  redelivery после удаления source без повторной preparation.
- `TestAudioUpgradeExecutorRetriesAfterInstalledResultRollbackPostgreSQL`
  (`service/publication_audio_upgrade_executor_integration_test.go`) сохраняет
  CURRENT lyrics и два изображения (JPEG front, PNG back). После фактической
  замены и SQL rollback старая DB generation/authority остаётся; повторная
  preparation завершается успешно, native readback подтверждает оба изображения
  и теги. Completed redelivery работает после удаления source и destination.
  Codec observations здесь контролируются transport fixture: это lifecycle
  evidence, а не доказательство реального codec upgrade.
- `TestTerminalPublicationSettlementUsesCurrentPersistedRiverState`,
  `TestTerminalPublicationSweepSettlesDiscardedAndMissingJobs` и
  `TestTerminalPublicationSweepDoesNotFailLiveRiverDelivery`
  (`persistence/newpublication_terminal_retry_integration_test.go`) проверяют
  видимую terminal failure и сохранение retry inputs, повторную проверку River
  state и запрет terminal settlement живой доставки. Сам scheduler River до
  исчерпания всех попыток этим не прогоняется.
- `TestRetryFailedPublicationAtomicallyEnqueuesAndRetainsFrozenAttempt`,
  `TestRetryFailedPublicationRollsBackInsertedRiverJob` и
  `TestConcurrentRetryFailedPublicationHasOneWinner` в том же файле проверяют
  ручной retry, rollback реальной вставки River job и единственного победителя.
- `TestRunJobCommandUnixCancellationAndInheritedPipeAreBounded`
  (`integrations/tools/job_command_unix_test.go`) проверяет отмену запущенного
  основного процесса и bounded ожидание унаследованного output pipe.
  `TestRunJobCommandUnixRetainsOutputWriterFailureAfterProcessExit` проверяет
  возврат ошибки output callback после завершения процесса.
  `TestSimplePublicationTempDirectoryCleanupRequiresCommandQuiescence`
  (`service/publication_simple_install_test.go`) запрещает cleanup приватных
  входов при неизвестном завершении команды.
- Независимое read-only ревью `ba29040` / `5d007d2` не нашло P1/P2:
  provider list mappings сохраняют порядок и evidence boundaries;
  ARTISTSORT наследуется отдельно от ALBUMARTISTSORT; native writer/readback
  проверяет mappings в FLAC/MP3/M4A/MKA. M4A validator сохраняет проверки
  authorized image MIME/codec, количества и topology.

## Оставшаяся runtime-проверка

На существующем приватном стенде запущена проверенная сборка с указанным выше
SHA-256 и revision `ba29040` (PID 29056). Прежний процесс `5d007d2` не используется
как свидетельство новой проверки.

Публичная AAC/M4A publication успешна: операция
`bd271524-3c88-48f1-857b-ada7462a07ec`, native readback AAC, 6903 packets;
ordered encoded-audio packet hashes совпали. Tags/artwork проверены.
Свидетельства — `format-acceptance.json` существующего stand
`plan12-cover-acceptance-1d3c7829-7b01-4c47-a9dc-7bd1b9f40ef1` во временном
каталоге OpenCode.

ALAC **не принят**: source scan и local confirmation завершились, но до timeout
не наблюдались publication attempt и CURRENT. Затем PostgreSQL стенда
`127.0.0.1:32777` стал отклонять соединения, API вернул HTTP 500, Docker API был
недоступен. Причина отсутствия публикации не установлена. Данные не сбрасывались;
скрипт восстановил `mka`, последнее подтверждённое значение cleanup — OFF.
Свидетельства: `evidence/alac-runtime-blocker.json` и
`evidence/alac-public-acceptance.json` в том же stand. Для продолжения нужно
восстановить доступ к существующей БД без reset и перечитать состояние релиза.

После запуска OrbStack владельцем существующий PostgreSQL-контейнер восстановился,
`/api/settings` вернул HTTP 200. Повторное чтение обнаружило успешную операцию
`e012b017-0a12-4793-b206-f1979d17ab2f`, но её CURRENT — MKA/FLAC, не ALAC.
PCM и cover совпали с source/receipt; этот результат не считается ALAC acceptance.
Для ALAC используется отдельный disposable fixture с сохранением `m4a` до
settlement; существующая публикация не изменяется.

V2 fixture выявил уже программный blocker: public preview возвращает
`verified ALAC channel and sample-depth capability is required`. В production
нет producer такой capability, а persisted source facts запрещают её `true`.
Это незавершённый технический gate, не отказ FFmpeg сохранить аудио. Его
замена реализуется как проверка конкретного готового private ALAC candidate:
равенство decoded PCM и сохранение actual audio shape до native exposure,
без generic capability cache и без присваивания источнику tool evidence.
До gate и повторной runtime-проверки эта правка не считается принятой.

## Последующее решение и исправление namespace

Независимая сверка полного scope обнаружила ограничение
`service/publication_first_namespace_candidates.go`: вложенные custom templates
отклоняются, хотя утверждённый контракт разрешает configurable relative paths.
Последующим решением владельца от 2026-10-26 entity-level конфликты вынесены в
отдельный план, а общие каталоги любых артистов/релизов временно разрешены для
всех templates. Добавлено построение directory candidates для произвольной
глубины rendered path. Shared candidates используют native-equivalent name,
не scope-first lookup; при reuse сохраняются existing canonical key и scope.
Это позволяет использовать существующие directory claims без миграции и не
объединяет разные имена каталогов из-за совпадения identity scope.
Файловая ownership и стандартный filename suffix flow сохраняются.
Исправление `0245cdb` прошло полный локальный gate обычного commit hook,
включая nested preview → confirmation → executor PostgreSQL regression,
и отправлено в `go-development`. Полная CI
[40305565660](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/40305565660)
выполняется. До завершения проверки ALAC и CI план остаётся в `todo`.

Boot recovery, journals, backup/rollback и resume не являются открытыми
пунктами: они исключены действующим решением владельца от 2026-10-23.
