# CURRENT preview: точный план lyrics

База — `7135c8ed`, полный hook и вся
[CI 42420678807](https://github.com/ruckus-dj/MusicEnreachment/actions/runs/42420678807)
успешны, включая контейнер. Повторный CI `525a1c50` также полностью успешен.

Только CURRENT comparison разрешает lyrics вне snapshot transaction по сохранённым
данным явно выбранного association/stream и captured LRCLIB enablement. Source
файлы не открываются, повторного извлечения metadata нет. Первая публикация и её
admission digest не меняются. Preview и executor используют общий resolver policy,
но существующая execution-семантика manual absent этим срезом не изменяется.

Manual value применяется побайтово; manually saved absent/explicit empty
подавляют acquisition. Invalid manual input не подменяется автоматическим.
Known absence отличается от unknown acquisition failure. Semantic digest
сохраняет authority/source/settings/lookup/result, но не transient warnings/timing.
CURRENT lyrics сравниваются отдельно от общих tags; raw presence/values сохранены.
Legacy empty/ambiguous evidence оставляет comparison unavailable без нового
правила эквивалентности. Ничего не выдаётся за overall unchanged или разрешённую
повторную публикацию.

Review выявило и помогло исправить accidental first-preview digest extension,
warning-dependent digest, неправильную manual fixture и stale UI message key.
Регрессии in-memory; новые database fixtures не добавлялись.

Браузер: непосредственно production preview component + I18nProvider, mocked API,
RU/EN, 1440×900 и 390×900. Планируемый текст сохранил переносы, двойные и trailing
пробелы; raw CURRENT JSON сохранил дубли/границы строк; unknown показан отдельно
от absent и equal. Overflow/confirm controls отсутствовали. Это не backend E2E
и не доказательство корректности deliberately malformed mocked CURRENT evidence.
Полный gate/CI нового среза ожидаются.
