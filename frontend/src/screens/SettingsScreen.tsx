import type { GenreCatalog, RuntimeSettingsDraft } from "../types";

export function SettingsScreen({
  draft,
  loading,
  saving,
  onChange,
  onSave,
  genres,
  genreSearch,
  onGenreSearch,
  genreLoading,
  genreSyncing,
  onSyncGenres,
}: {
  readonly draft: RuntimeSettingsDraft | null;
  readonly loading: boolean;
  readonly saving: boolean;
  readonly onChange: (draft: RuntimeSettingsDraft) => void;
  readonly onSave: () => void;
  readonly genres: GenreCatalog | null;
  readonly genreSearch: string;
  readonly onGenreSearch: (value: string) => void;
  readonly genreLoading: boolean;
  readonly genreSyncing: boolean;
  readonly onSyncGenres: () => void;
}) {
  if (loading || !draft) return <div className="empty-state">Загрузка настроек…</div>;
  const update = <K extends keyof RuntimeSettingsDraft>(key: K, value: RuntimeSettingsDraft[K]) =>
    onChange({ ...draft, [key]: value });
  const visibleGenres = (genres?.items ?? []).filter((genre) =>
    `${genre.display_name} ${genre.source_name}`.toLowerCase().includes(genreSearch.toLowerCase()),
  );
  return (
    <section className="settings-screen">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Runtime configuration</p>
          <h2>Настройки сервиса</h2>
        </div>
        <button type="button" className="primary" disabled={saving} onClick={onSave}>
          {saving ? "Сохраняем…" : "Сохранить настройки"}
        </button>
      </div>
      <div className="settings-grid">
        <fieldset className="settings-card">
          <legend>Обработка и matching</legend>
          <label>
            Порог совпадения
            <input
              type="number"
              min="0"
              max="1"
              step="0.01"
              value={draft.confidence_threshold}
              onChange={(event) => update("confidence_threshold", Number(event.target.value))}
            />
          </label>
          <label>
            Таймаут, секунд
            <input
              type="number"
              min="1"
              max="120"
              value={draft.timeout_seconds}
              onChange={(event) => update("timeout_seconds", Number(event.target.value))}
            />
          </label>
          <label>
            Задержка retry, секунд
            <input
              type="number"
              min="0"
              max="3600"
              value={draft.retry_delay_seconds}
              onChange={(event) => update("retry_delay_seconds", Number(event.target.value))}
            />
          </label>
          <label>
            Максимум попыток
            <input
              type="number"
              min="1"
              max="10"
              value={draft.max_attempts}
              onChange={(event) => update("max_attempts", Number(event.target.value))}
            />
          </label>
        </fieldset>
        <fieldset className="settings-card">
          <legend>Внешние провайдеры</legend>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.musicbrainz_enabled}
              onChange={(event) => update("musicbrainz_enabled", event.target.checked)}
            />{" "}
            MusicBrainz включён
          </label>
          <label>
            User-Agent MusicBrainz
            <input
              value={draft.musicbrainz_user_agent}
              onChange={(event) => update("musicbrainz_user_agent", event.target.value)}
            />
          </label>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.acoustid_enabled}
              onChange={(event) => update("acoustid_enabled", event.target.checked)}
            />{" "}
            AcousticID включён
          </label>
          <label>
            Client key AcousticID
            <input
              type="password"
              placeholder="Пусто — сохранить текущий"
              value={draft.acoustid_client_key}
              onChange={(event) => update("acoustid_client_key", event.target.value)}
            />
          </label>
          <label className="settings-check">
            <input
              type="checkbox"
              checked={draft.artwork_enabled}
              onChange={(event) => update("artwork_enabled", event.target.checked)}
            />{" "}
            Artwork enrichment включён
          </label>
        </fieldset>
        <fieldset className="settings-card settings-card-wide">
          <legend>Жанры MusicBrainz</legend>
          <div className="genre-catalog-toolbar">
            <input
              aria-label="Поиск жанра"
              placeholder="Найти жанр по названию"
              value={genreSearch}
              onChange={(event) => onGenreSearch(event.target.value)}
            />
            <button
              type="button"
              className="secondary"
              disabled={genreSyncing}
              onClick={onSyncGenres}
            >
              {genreSyncing ? "Обновляем…" : "Обновить каталог"}
            </button>
          </div>
          {genreLoading ? (
            <p className="settings-help">Загрузка каталога…</p>
          ) : visibleGenres.length ? (
            <ul className="genre-catalog" aria-label="Каталог жанров">
              {visibleGenres.slice(0, 80).map((genre) => (
                <li className="genre-chip" key={genre.musicbrainz_id}>
                  <strong>{genre.display_name}</strong>
                  <small>{genre.source_name}</small>
                </li>
              ))}
            </ul>
          ) : (
            <p className="settings-help">Каталог пуст. Нажмите «Обновить каталог».</p>
          )}
          <small>
            Названия берутся из MusicBrainz; исходные значения сохраняются для точного маппинга.
          </small>
        </fieldset>
      </div>
    </section>
  );
}
