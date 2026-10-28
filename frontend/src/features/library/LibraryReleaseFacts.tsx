import type {
  LibraryReleaseDetailResponse,
  LibraryReleaseTrackResponse,
} from "../../api/generated/client.schemas";

type MetadataField = { state?: unknown; value?: unknown };

function metadataFields(metadata: unknown) {
  if (!metadata || typeof metadata !== "object") return {};
  const local = (metadata as Record<string, unknown>).local_metadata;
  if (!local || typeof local !== "object") return {};
  const envelope = local as Record<string, unknown>;
  if (envelope.scope !== "release" || !Number.isInteger(envelope.version))
    return {};
  const desired = envelope.desired;
  return desired && typeof desired === "object" && !Array.isArray(desired)
    ? (desired as Record<string, MetadataField>)
    : {};
}

function fieldValue(field: MetadataField | undefined) {
  if (!field || typeof field !== "object") return "Не указано";
  if (field.state === "explicit_empty") return "Явно очищено";
  if (field.state !== "value") return "Не указано";
  const value = field.value;
  if (typeof value === "string") return value || "Не указано";
  if (Array.isArray(value)) {
    const items = value.filter(
      (item): item is string => typeof item === "string",
    );
    return items.length ? items.join(", ") : "Не указано";
  }
  return "Не указано";
}

function positionNumber(track: LibraryReleaseTrackResponse) {
  return track.displayed_number.trim() || String(track.position);
}

function formatDuration(duration: number | null) {
  if (duration === null) return "Не указана";
  const seconds = Math.floor(duration / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}${duration % 1000 ? `.${String(duration % 1000).padStart(3, "0")}` : ""}`;
}

export function ReleaseCreditList({
  credits,
}: {
  credits: LibraryReleaseDetailResponse["artists"];
}) {
  if (!credits?.length) return <span>Исполнители не указаны</span>;
  const creditKeyCounts = new Map<string, number>();
  return (
    <ul className="library-release-credits">
      {credits.map((credit) => {
        const signature = `${credit.artist_id}-${credit.role}-${credit.join_phrase}-${credit.credit_name}`;
        const occurrence = creditKeyCounts.get(signature) ?? 0;
        creditKeyCounts.set(signature, occurrence + 1);
        const displayName = credit.credit_name || credit.artist_name;
        const canonicalName =
          credit.artist_name && credit.artist_name !== displayName
            ? credit.artist_name
            : null;
        return (
          <li key={`${signature}-${occurrence}`}>
            <a
              href={`#/library/artists/${encodeURIComponent(credit.artist_id)}`}
            >
              {displayName}
            </a>
            {canonicalName ? (
              <>
                {" ("}
                <span className="library-release-credit-identity">
                  {canonicalName}
                </span>
                {")"}
              </>
            ) : null}
            {credit.join_phrase}
            {credit.role ? (
              <span className="library-release-credit-role">
                {" "}
                · {credit.role}
              </span>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

export function ReleaseMetadataFacts({ metadata }: { metadata: unknown }) {
  const desired = metadataFields(metadata);
  const fields = [
    ["Дата", "release_date"],
    ["Страна", "release_country"],
    ["Лейбл", "label"],
    ["Номер каталога", "catalog_number"],
    ["Тип релиза", "release_primary_type"],
  ] as const;
  return (
    <dl
      className="library-release-facts"
      aria-label="Сохранённые сведения о релизе"
    >
      {fields.map(([label, key]) => (
        <div key={key}>
          <dt>{label}</dt>
          <dd>{fieldValue(desired[key])}</dd>
        </div>
      ))}
    </dl>
  );
}

export function ReleaseMediaSummary({
  release,
}: {
  release: LibraryReleaseDetailResponse;
}) {
  const media = release.media ?? [];
  const tracks = release.tracks ?? [];
  return (
    <section
      className="library-release-media"
      aria-labelledby="release-media-heading"
    >
      <h2 id="release-media-heading">Носители и позиции</h2>
      {media.length ? (
        <section
          className="library-release-table-scroll"
          aria-label="Позиции по носителям"
        >
          <table>
            <thead>
              <tr>
                <th scope="col">Носитель</th>
                <th scope="col">Название</th>
                <th scope="col">Формат</th>
                <th scope="col">Позиции</th>
              </tr>
            </thead>
            <tbody>
              {media.map((medium) => {
                const items = tracks.filter((track) =>
                  track.medium_id
                    ? track.medium_id === medium.id
                    : track.medium === medium.medium,
                );
                return (
                  <tr key={medium.id}>
                    <th scope="row">{medium.medium}</th>
                    <td>{medium.title || "Без названия"}</td>
                    <td>{medium.format || "Не указан"}</td>
                    <td>
                      {items.length ? (
                        <ul className="library-release-position-links">
                          {items.map((track) => (
                            <li key={track.id}>
                              <a
                                href={`#/library/releases/${encodeURIComponent(release.id)}/positions/${encodeURIComponent(track.id)}`}
                              >
                                {positionNumber(track)} · {track.title}
                              </a>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <span>Позиции не указаны</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ) : (
        <p>Сведения о носителях не сохранены.</p>
      )}
      {!media.length && tracks.length > 0 ? <p>Позиции релиза:</p> : null}
    </section>
  );
}

export function PositionIdentity({
  track,
}: {
  track: LibraryReleaseTrackResponse;
}) {
  return (
    <dl
      className="library-release-position-facts"
      aria-label={`Сведения о позиции ${positionNumber(track)}`}
    >
      <div>
        <dt>Позиция</dt>
        <dd>{positionNumber(track)}</dd>
      </div>
      <div>
        <dt>Название позиции</dt>
        <dd>{track.title || "Не указано"}</dd>
      </div>
      <div>
        <dt>Длительность</dt>
        <dd>{formatDuration(track.duration_ms)}</dd>
      </div>
      <div>
        <dt>Recording</dt>
        <dd>{track.recording_title || "Не указан"}</dd>
      </div>
      <div>
        <dt>Recording ID</dt>
        <dd>{track.recording_id || "Не указан"}</dd>
      </div>
    </dl>
  );
}

export function PositionSources({
  track,
}: {
  track: LibraryReleaseTrackResponse;
}) {
  const sources = track.sources ?? [];
  return (
    <section
      aria-label={`Источники аудио позиции ${positionNumber(track)}`}
      className="library-release-sources"
    >
      <h3>Источники аудио</h3>
      {sources.length ? (
        <section
          className="library-release-table-scroll"
          aria-label="Сведения об источниках аудио"
        >
          <table>
            <thead>
              <tr>
                <th scope="col">Вариант</th>
                <th scope="col">Решение</th>
                <th scope="col">Совместимость</th>
                <th scope="col">Политика</th>
                <th scope="col">Расположения</th>
              </tr>
            </thead>
            <tbody>
              {sources.map((source) => (
                <tr key={source.media_variant_id}>
                  <th scope="row">{source.media_variant_id}</th>
                  <td>
                    {source.selected ? "Выбран" : "Альтернатива"} ·{" "}
                    {source.decision_method || "не указан"}
                  </td>
                  <td>{source.compatible ? "Совместим" : "Несовместим"}</td>
                  <td>{source.policy_version || "Не указана"}</td>
                  <td>
                    {source.locations?.length ? (
                      <ul className="library-release-location-list">
                        {source.locations.map((location) => (
                          <li key={location.location_id}>
                            <a
                              href={`#/sources/${encodeURIComponent(location.root_id)}`}
                            >
                              {location.relative_path}
                            </a>
                            <span>
                              {" "}
                              · корень {location.root_status}
                              {location.root_enabled
                                ? " (включён)"
                                : " (выключен)"}{" "}
                              · проверка {location.probe_status} · изменение{" "}
                              {location.mtime || "не известно"} · размер{" "}
                              {location.size_bytes} байт
                            </span>
                          </li>
                        ))}
                      </ul>
                    ) : (
                      <span>Расположения источника не указаны.</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ) : (
        <p>Для этой позиции нет сохранённых сведений об источниках.</p>
      )}
    </section>
  );
}
