type DashboardScreenProps = {
  readonly scanning: boolean;
  readonly reprocessing: boolean;
  readonly refreshingMetadata: boolean;
  readonly reconcilingPublications: boolean;
  readonly onScan: () => void;
  readonly onReprocessAll: () => void;
  readonly onRefreshMetadata: () => void;
  readonly onReconcilePublications: () => void;
};

export function DashboardScreen({
  scanning,
  reprocessing,
  refreshingMetadata,
  reconcilingPublications,
  onScan,
  onReprocessAll,
  onRefreshMetadata,
  onReconcilePublications,
}: DashboardScreenProps) {
  const busy = scanning || reprocessing || refreshingMetadata || reconcilingPublications;
  return (
    <section className="settings-screen dashboard-screen" aria-labelledby="dashboard-heading">
      <div className="screen-heading">
        <div>
          <p className="eyebrow">Управление</p>
          <h2 id="dashboard-heading">Действия с{"\u00a0"}медиатекой</h2>
        </div>
      </div>
      <div className="settings-grid">
        <fieldset className="settings-card">
          <legend>Обработка источников</legend>
          <p className="settings-help">
            Запустите поиск новых файлов, массовую переобработку или обновление подтверждённых пар.
          </p>
          <div className="provider-actions dashboard-source-actions">
            <button type="button" className="primary" disabled={busy} onClick={onScan}>
              {scanning ? "Сканируем…" : "Сканировать новые и\u00a0изменённые"}
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={onReprocessAll}>
              {reprocessing ? "Ставим в очередь…" : "Переобработать всю медиатеку"}
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={onRefreshMetadata}>
              {refreshingMetadata
                ? "Ставим пары в очередь…"
                : "Обновить подтверждённые пары MusicBrainz"}
            </button>
          </div>
        </fieldset>
        <fieldset className="settings-card">
          <legend>Папка публикаций</legend>
          <p className="settings-help">
            Удаляет файлы без публикации в БД. Отсутствующие актуальные публикации помечаются
            неактивными и ставятся в очередь повторно.
          </p>
          <div className="provider-actions">
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={onReconcilePublications}
            >
              {reconcilingPublications ? "Проверяем публикации…" : "Проверить папку публикаций"}
            </button>
          </div>
        </fieldset>
      </div>
    </section>
  );
}
