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
