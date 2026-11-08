# Retained source: публичный preview/confirm

База `e87a164b` прошла полный hook и CI 42518700580. Этот срез открывает только
`retained_same_path`: уже выбранный source, известная authority и прежний путь.
Explicit source selection, изменённый путь, отсутствующая authority и downgrade
acknowledgement остаются вне среза; пункт 3 общего плана не закрывается.

GET/POST используют явный workflow. Retained confirmation требует mode,
expected generation, epoch и digest; source selectors запрещены. Старые first
defaults сохранены. Capability различает ready/unchanged/blocked. Ordinary
unchanged не enqueue; forced готовит новый candidate, но не обходит quality guard.

Native CURRENT и frozen v4 inputs захватываются до выдачи digest. Admission
повторяет подготовку preview и в authoritative callback сверяет полный runtime
settings snapshot и заново вычисленный pure planning digest. Planned и actual
quality должны доказать equal/better; unknown и downgrade блокируются.

Known-error после MOVE/DB rollback не принимает final как готовый результат:
retry заново готовит source candidate. Reprepare outcome разрешается только для
точного quiescent v4 владельца; changed final инспектируется как replacement
witness. Отсутствующий final устанавливается через RenameNoReplace. Missing
interrupted target возвращается к preparation только после guarded проверки
authority и namespace; symlink/permission ошибки не считаются отсутствием.

UI связывает workflow/mode/generation с запросами и подтверждением, сохраняя
pending/uncertain fences. Component browser checks с API fixtures проверили RU/EN,
1440/390, ready/unchanged/blocked, отсутствие overflow и confirmation payload.
Это не real-backend/full-shell E2E. Независимые reviews service/API/UI/retry нашли
и помогли исправить неполное сравнение settings и устаревшую generation fixture.
Native сценарий расширен same-process retry после MOVE и missing-final retry;
source-gone interrupted adoption сохранён отдельно в том же DB fixture.

`task generate` выполнен. Первый `task verify` остановился на unstaged generated
diff до lint/tests: check-generated сравнивает working tree с index. Обычный
commit hook после staging должен подтвердить весь gate; CI среза ещё ожидается.
