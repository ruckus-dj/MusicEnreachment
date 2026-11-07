# Read-only сравнение CURRENT: предпосылка republish

База — `98d21f1b`, прошедшая полный hook и всю
[CI 42415924806](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42415924806).
Пункт 3 плана 13 не закрывается этим срезом.

Существующий position preview дополнен ordinary/forced mode, включённым в digest.
Для CURRENT возвращаются generation witness, точные различия сохранённых тегов
с сохранением порядка/пробелов/presence, текущий, rendered и resolved пути.
Comparison namespace не подменяет новый basename прежним retry filename;
исключение даёт только exact CURRENT claim, не другой same-position claim.

Подтверждение CURRENT недоступно в UI и по-прежнему запрещено typed first
admission. AUDIO отмечено как factual-only, assets остаются не полностью
сравнёнными. Общий `unchanged` не утверждается. Нет новых write producers,
миграций или изменений automatic upgrade/rewrite.

Production controller сохраняет fences pending/uncertain admission; mode,
source и CURRENT generation инвалидируют неподтверждённый preview.
Текст компонента локализован RU/EN. Срез не является готовым forced republish:
дальше нужны exact cover/lyrics inputs и typed replacement admission/execution.

## Проверки до полного gate

Независимое review выявило и помогло исправить отсутствие CURRENT generation
в refresh identity, неверный namespace fallback при missing CURRENT claim и
слияние null/empty отображения. Новые регрессии минимальны и используют
существующие pure planner/UI fixtures.

В браузере смонтирован непосредственно production `FirstPublicationPreview`
с `I18nProvider`, без полного release shell, с origin-anchored API fixture.
При 1440×900 и 390×900, RU/EN проверены ordinary→forced GET-запросы,
раздельные пути, порядок/пробелы tags и null/[]; overflow отсутствовал.
Единственная кнопка — preview; POST/admission не выполнялись.
Это component-level проверка, не real-backend E2E. Полный gate/CI ещё ожидаются.

## Полный gate и CI

`525a1c50` прошёл обычный полный commit hook. В первом запуске
[CI 42419169960](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42419169960)
Windows migration suite сообщил PASS, но cleanup завершился ошибкой supervisor:
`force-terminated without confirming PostgreSQL cleanup`. Остальные platform jobs,
verify и Compose smoke прошли; container job не запускался из-за Windows failure.
Failed jobs перезапущены без изменения gates или таймаутов. Повторный Windows
прогон и публикация контейнера завершились успешно; полный CI теперь зелёный.
