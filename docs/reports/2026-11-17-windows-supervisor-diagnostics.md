# Windows supervisor: согласованный диагностический прогон

## Основание

CI `44001037696` (`f8b42863`) повторила неподтверждённое завершение supervisor
embedded-postgres; PostgreSQL сообщил shutdown, но API package завершился ошибкой
cleanup. Ранее аналогичный сбой наблюдался на `a053c93d`. Точная причина не доказана.
Владелец явно согласовал подготовку и запуск диагностического CI сообщением «Делай»
после предложения bounded ручного прогона. Это не продуктовое изменение и не fix.

## Границы эксперимента

Opt-in workflow_dispatch собирает instrumented supervisor из той же pinned
alpha.3 revision. Сохраняются обычная test command, concurrency, таймауты,
Windows DACL и правила ошибки при неподтверждённом cleanup. Существующий upstream
EP_SUPERVISOR применяется только к диагностическому test step; новых product env
и dependency upgrades нет. Ручной прогон не публикует контейнер.

Source archive и patch/preimages проверяются по SHA-256; provenance включает
instrumented binary hash. Sparse phase traces пишутся вне disposable cluster,
не в stdout protocol и без credentials/полных argv. Успешный instrumented run
не доказывает исправление и не заменяет обычную Windows CI.

## Статус

Реализация CI-only tooling выполняется после `f8b42863`; независимое ревью,
полный hook и сам диагностический прогон ещё не выполнены.

Независимое ревью выявило blocker в генерации Go: обычный JS template literal
поглощал escapes JSON format string. Исправляется raw template и добавляется
assertion на фактически сгенерированную строку. Успешное форматирование исходного
template не подменяет проверку полученного Go source. Dispatch ещё не запускался.

Blocker исправлен через `String.raw`; regression читает фактический exported
generated source. Этот source извлечён без выполнения helper preparation и принят
`gofmt` без изменений. Проверка не является build/test gate. Остальные результаты
ревью блокеров не содержат; следующий этап — обычный полный commit hook.

`34f620b9` прошёл обычный полный hook и отправлен. С согласия владельца запущен
workflow_dispatch `44094826529` с `windows_postgres_diagnostics=true`.
Отдельная обычная push CI `44094826875` выполняется без instrumentation override.
Результаты обоих прогонов пока не подтверждены; диагностический успех не будет
считаться доказанным исправлением или заменой обычного Windows gate.

Dispatch `44094826529` завершился ошибкой до Windows integration tests: prep
step молча вышел 0 без outputs, fail-closed guard запретил release-helper fallback,
artifact upload не получил path. Диагностические traces не получены; это сбой
нашей обвязки, а не воспроизведение PostgreSQL shutdown.

Причина установлена: entry guard сравнивал filesystem path с file URL и никогда
не вызывал main. Сравнение приведено к одному URL-представлению, добавлен минимальный
regression actual module path / import / Windows URL case. Fail-closed guard
сохранён. Исправление ещё не прошло полный hook; новый dispatch не запускался.

`f2b9654d` прошёл полный обычный hook и отправлен. Исправленный dispatch
`44097196024` и обычная CI `44097195359` запущены; результаты пока ожидаются.
Предыдущая обычная CI `44094826875` для `34f620b9` полностью зелёная, включая
Windows и контейнер. Этот результат не устанавливает причину intermittent
shutdown failure и не подменяет отсутствующие diagnostic traces.

Исправленный dispatch `44097196024`: Windows job `134087349790` полностью
успешен, instrumented helper построен и проверен, integration tests и artifact
upload прошли. Артефакты скачаны в
`final-acceptance-20261117/diagnostics-44097196024` для независимого анализа.
Исходный shutdown failure этим успешным прогоном не воспроизведён.

Весь manual workflow failed: macOS arm64 job `134087350141` завершился за
15m1s без отображённых steps; failed log пуст. Причина этого отдельного job пока
не подтверждена. Результат Windows не подменяет полный workflow result.

Независимый разбор artifact `14335870920` установил: ZIP содержит только
`provenance.json` и `UPSTREAM-LICENSE`, JSONL traces отсутствуют (upload log: два
файла). EP_SUPERVISOR и pinned build provenance подтверждены, но runtime collection
не доказана; lifecycle timings/errors не измерены. Прежнее сообщение о полученных
артефактах не означает наличия phase evidence. Исправляются collection и обязательный
native preflight получения main-entry/main-exit до публикации helper output.

macOS arm64 уточнён по job/check metadata: runner_id=0, steps=[]; annotation
«The job was not acquired by Runner of type hosted even after multiple attempts».
Это runner acquisition failure. Новый неизменённый manual rerun не выполнялся.

Обычная CI `44097195359` для `f2b9654d` полностью зелёная с первого attempt,
включая Windows и контейнер, без diagnostic override. Исправление collection
продолжается отдельно; зелёная обычная CI не доказывает причину прежнего сбоя.

Collection repair готов к ревью: linker `-X` получает quoted whole assignment,
а не JSON quotes внутри значения. После native build добавлен обязательный binary
preflight: до supervisor output требуются реальные JSONL main-entry/main-exit,
trace hash включается в provenance. Focused pure regression cases добавлены;
полный hook и Windows-прогон этой правки ещё не выполнялись.

Независимое ревью collection repair блокеров не выявило. По Go quoted splitting
прежние embedded JSON quotes сохранялись как буквальные символы baked path,
что объясняет file-open failures даже у пути без пробелов. Новое whole-assignment
quoting и native binary preflight проверяют реальную запись. JSONL bytes не
переписываются: provenance hash хранит исходный текст с nanosecond timestamps.

`232327d2` прошёл обычный полный hook и отправлен. Обычная CI `44101636653`
полностью зелёная, включая Windows и контейнер. Отдельный diagnostic dispatch
`44101637411` завершился failure: Windows job `134102903868` прошёл native trace
preflight и require guard, упал на integration tests; artifact upload успешен.
Остальные выполненные jobs зелёные, container publish skipped. Artifact и failed
log скачаны в `final-acceptance-20261117/diagnostics-44101637411` и переданы на
независимый read-only разбор. До чтения результатов не утверждается, что это
повторение прежнего shutdown failure. Новый rerun и production shutdown changes
не выполнялись.

### Разбор dispatch `44101637411` на `232327d2`

Диагностика collection теперь подтверждена: 50 JSONL / 283 события. Preflight
`8276.jsonl` (2 события, hash совпадает с provenance) отделён от integration:
49 traces / 281 событие, включая 9 PostgreSQL server lifecycles и 40 command
lifecycles. Nanosecond timestamps разобраны Python integers без потери точности.
Helper hash совпадает с committed script после Windows CRLF normalization;
patch/source/license hashes проверены, EP_SUPERVISOR соответствует provenance.
Binary и archive отсутствуют в artifact: их записанные hashes независимо не
пересчитаны.

Этот прогон упал **не на PostgreSQL shutdown**. `failed-job.log:71–78` содержит
три dispatch deadline failure в move-worker tests:
`OldSourceStagingCollisionPreservesUnknownData/source_1` (67.19s) и
`PreservesIndependentSourceAfterRootSwitch/source_0` / `source_1` (61.61s / 64.88s).
Это существующий 60s deadline `dispatchMove` (`move_worker_test.go:874–887`),
а не PostgreSQL watchdog. Repositories в этих тестах in-memory, version runner
fake, filesystem настоящий. Два последних случая дошли до `running/switched`;
при отменённом context `rollbackSwitched` возвращает `ctx.Err()` до записи
`rollback_pending`, маскируя исходную cleanup cause. Combined assertion label
«unknown source was adopted» не доказывает adoption: отдельные проверки
сохранности inode/content не упали.

Все 9 server traces имеют успешные pg_ctl return, PostgreSQL wait, owner-EOF
stop completion, supervisor return и main-exit=0; применимых пропущенных endings
нет. Timeout/context-ended/kill events отсутствуют. Owner-EOF → main-exit:
8020 — 6.593065600s; 10168 — 1.313699400s; 9808 — 0.901157800s;
1888 — 5.950171000s; 1596 — 6.920281300s; 4384 — 5.201644300s;
5688 — 1.616437600s; 7008 — 7.028886000s; 7712 — 0.848761800s.
Максимальный pg_ctl runtime 7.024216600s ниже 10s shutdown deadline и 12s
parent watchdog. Jobs cleanup 22.62s включает remove_and_release 20.997s:
это не длительность supervisor shutdown. Связь с PID 5688 timing-based,
поскольку package/test-owner labels в traces отсутствуют.

Два command traces (`5036.jsonl`, `4412.jsonl`) завершились child exit 2 /
helper exit 1, с полными lifecycle endings. Их команды не записаны, связь с
test failures не установлена. Buffered output timestamps не позволяют точно
привязать subtest failure к PID.

Итог: исходный intermittent supervisor shutdown failure не воспроизведён и не
исправлен. Причина move dispatch latency пока неизвестна; filesystem latency,
scheduling contention и влияние instrumentation — гипотезы, не диагноз.
Следующая минимальная диагностика — deadline-time goroutine stacks в
`dispatchMove` и исходная rollback cause/stage/elapsed в `rollbackSwitched`.
Новых dispatch/rerun не выполнено; shutdown logic, deadlines и gates не ослаблены.
