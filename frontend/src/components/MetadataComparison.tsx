import type { Tags } from "../types";

export function MetadataComparison({
  fields,
  originalTags,
  analyzedTags,
  draft,
  editing,
  saving,
  onEdit,
  onCancel,
  onChange,
  onSave,
}: {
  readonly fields: readonly string[];
  readonly originalTags: Tags;
  readonly analyzedTags: Tags;
  readonly draft: Tags;
  readonly editing: boolean;
  readonly saving: boolean;
  readonly onEdit: () => void;
  readonly onCancel: () => void;
  readonly onChange: (field: string, value: string) => void;
  readonly onSave: () => void;
}) {
  return (
    <section className="metadata-card comparison-card" aria-labelledby="metadata-comparison-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Metadata inspector</p>
          <h2 id="metadata-comparison-title">Что попадёт в релиз</h2>
        </div>
        {!editing ? (
          <button type="button" className="secondary" onClick={onEdit}>
            Редактировать
          </button>
        ) : (
          <div className="provider-actions">
            <button type="button" className="secondary" disabled={saving} onClick={onCancel}>
              Отменить
            </button>
            <button type="button" className="primary" disabled={saving} onClick={onSave}>
              {saving ? "Сохраняем…" : "Сохранить"}
            </button>
          </div>
        )}
      </div>
      <p className="comparison-intro">
        Исходные теги не меняются. Значения в колонке «Текущий» попадут в следующую публикацию.
      </p>
      <div className="metadata-comparison-scroll">
        <span className="metadata-scroll-hint" aria-hidden="true">
          Листайте вправо →
        </span>
        <table className="metadata-comparison">
          <caption className="visually-hidden">Сравнение метаданных</caption>
          <thead>
            <tr className="metadata-comparison-row metadata-comparison-header">
              <th scope="col">Тег</th>
              <th scope="col">Исходный</th>
              <th scope="col">Из анализа</th>
              <th scope="col">Текущий</th>
            </tr>
          </thead>
          <tbody>
            {fields.map((field) => (
              <tr className="metadata-comparison-row" key={field}>
                <th className="metadata-field" scope="row">
                  {field}
                </th>
                <td className="metadata-value">{originalTags[field] || "—"}</td>
                <td className="metadata-value">{analyzedTags[field] || "—"}</td>
                <td className="metadata-value metadata-current">
                  {editing ? (
                    <input
                      aria-label={`Текущий тег ${field}`}
                      value={draft[field] ?? ""}
                      onChange={(event) => onChange(field, event.target.value)}
                    />
                  ) : (
                    draft[field] || "—"
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="editor-footer">
        <span className="muted">
          {editing
            ? "Редактируется только колонка «Текущий»."
            : "Текущие значения доступны для публикации."}
        </span>
      </div>
    </section>
  );
}
