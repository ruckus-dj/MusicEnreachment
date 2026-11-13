# Manual pickers: RU/EN

Следующий ограниченный срез открытого плана 13 после `35d57845`.

## Scope

`ManualSearchPicker` и `ManualReleasePicker`: собственные headings, labels,
доступные имена, paging/status и fallback messages. Собственные validation
ошибки хранят semantic identity и переводятся при render. Server detail,
`safe_error`, неизвестные `Error.message`, музыкальные данные и IDs остаются
без автоматического перевода.

Query, kind, page, operation и session guards сохраняются. Locale не запускает
запросы и не сбрасывает drafts; operation follower и API contract не меняются.
Grouping controls, evidence inspector и tag overrides — отдельные следующие
срезы, не часть этой приёмки.

## Статус

Реализация четырёх frontend файлов завершена и отформатирована. Один focused
locale-toggle сценарий проверяет query/kind/page, текущий результат и отсутствие
дополнительных запросов, а также перевод собственного invalid-payload fallback.
Независимое ревью выявило скрытый detail-less admission fallback в search picker:
alert condition не учитывал semantic `errorKey`. Условие и минимальная regression
проверка исправляются; release picker уже учитывает ключ корректно.
Runtime-проверки текущего среза ещё не проводились;
предыдущие зелёные CI не являются его проверкой. План 13 остаётся открытым.

Первый обычный hook остановился на новом picker regression: после перехода
на вторую страницу, ещё до locale flip, result-read counter равен 3 вместо
ожидаемых 2; admission/operation counters равны 2 и страница отображается.
Backend suite прошёл. Причина дополнительного чтения исследуется отдельно;
увеличение ожидаемого счётчика без объяснения не является исправлением.
Commit не создан, production behavior и gates не менялись.

Архитектурный разбор со сравнением `HEAD 35d57845` подтвердил существовавший
переход: pagination сохраняет старый operation ID, но уже заменяет request body
и `request_id`. Query key кратко становится `[old operation, new request_id]`,
порождая дополнительный GET старых результатов; он может быть отменён, поэтому
ровно три чтения также не являются инвариантом. Локализация эти переходы не
меняет. Production query/admission redesign не включается в этот срез.

Regression уточняется: дождаться success/error и idle точного query key новой
операции, затем зафиксировать counters/history и сравнить их после locale flip.
Для malformed третьей страницы требуется error именно operation-3, чтобы
transient offset mismatch старой операции не подменял проверяемую validation.
