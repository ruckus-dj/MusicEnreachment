# Frozen source republish: private preparation

База — `ebe83bfb`, полный hook и вся
[CI 42422510026](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42422510026)
успешны, включая контейнер. Пункт 3 плана 13 остаётся открытым.

Добавлен строгий v4 frozen-input contract: CURRENT witness, явный ordinary/forced
mode, source authority/association, exact lyrics/raw authority/lookup evidence,
selected original cover и planned PNG derivative. Старые версии не принимают
это расширение. Clone отсоединяет вложенные карты/срезы/байты. PNG проверяется
полным декодированием после ограниченного dimension-header preflight.

Новый private preparation path всегда готовит candidate из выбранного source,
применяя frozen tags/lyrics/PNG без resolver/converter/acquisition. CURRENT не
становится источником metadata. Optional lyrics и cover failures независимы:
lyrics fallback повторно применяет успешно записанную frozen cover, outcomes
перечитываются. Cancellation/unknown quiescence запрещают destructive fallback.

Срез намеренно fail-closed: `SupportsExecution` для v4 false, generic admission
отказывает до транзакции. Public executor, confirmation и producers не включены.
Новых таблиц/миграций нет; это предпосылка typed replacement authority, не готовая
повторная публикация.

Review выявило и помогло исправить слабые provenance/derivation combinations,
mandatory-tag сравнение optional lyrics, потерю cover при lyrics fallback,
неверную native topology fixture и неполную PNG-валидацию. Новая native fixture
не использует БД. Полный gate и CI этого среза ожидаются.

Проверка изолирована в отдельном worktree: параллельные Windows/UCRT64 и build
изменения основного дерева в этот commit не включаются и не откатываются.
