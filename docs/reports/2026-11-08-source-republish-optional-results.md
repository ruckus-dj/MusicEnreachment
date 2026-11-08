# Source republish: truthful optional results

База `8dfa014b` прошла полный hook и всю CI 42482776124, включая контейнер.

Private preparation передаёт typed transient receipt итогов lyrics/cover,
согласованный с final native readback после fallback. Persistence проверяет
receipt, frozen selection и actual assets вместе. Optional failure не становится
обязательным embedding: audio может быть committed без optional asset с честным
диагностическим summary. Непредусмотренный writer outcome — internal error,
не выдуманный derivative failure.

Manual value fallback не создаёт applied receipt и остаётся pending; сохранённый
manual absent не подменяется explicit-empty и не создаёт выдуманный receipt.
Summary может честно назвать manual source без утверждения applied. Cover
requested original и embedded derivative остаются раздельными.

Миграция `20261108000001_source_republish_cover_preparation_result` расширяет
существующий v2 cover projection CHECK на preparation_failed с четырьмя bounded
codes и без embedded facts. V1 правила не меняются; rollback отказывает при
сохранённых новых outcomes. Таблицы не добавляются. Inventory/schema assertions
обновлены; migration retention и result scenarios используют существующие fixtures.

Независимое review помогло исправить fallback tag comparison, неправильную
projection version, отсутствие новых receipts в старых fixtures и имя Go-типа.
Новые проверки минимальны: native fixture дополнена receipts, grouped DB fixture
проверяет fallback/manual absent/preparation failure и противоречивый receipt.
Полный hook и CI нового среза ожидаются.

Первый полный hook выявил две ошибки fixtures: original cover имел неверный MIME,
а rollback inventory не учитывал уже успешно отменённую новую миграцию перед
отказом старой. Исправлены только fixtures/assertions; обе focused диагностики
после упавшего gate прошли. Для frozen conversion failure persisted original
остаётся валидным: ошибка подготовки не даёт права подделывать authority bytes.

V4 всё ещё не активирован: SupportsExecution false, generic admission закрыт,
public confirmation и executor dispatch не подключены. Это не приёмка пункта 3.
