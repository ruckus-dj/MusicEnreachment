import type { Tags } from "../types";

export function OriginalTags({ tags }: { readonly tags: Tags }) {
  const entries = Object.entries(tags);
  return (
    <section className="evidence-card original-tags">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Источник</p>
          <h2>Исходные теги</h2>
        </div>
        <span className="badge">только чтение</span>
      </div>
      {entries.length ? (
        <div className="tag-grid">
          {entries.map(([name, value]) => (
            <div className="tag-value" key={name}>
              <span>{name}</span>
              <strong>{value}</strong>
            </div>
          ))}
        </div>
      ) : (
        <p className="candidate-empty">Исходные теги не найдены.</p>
      )}
    </section>
  );
}
