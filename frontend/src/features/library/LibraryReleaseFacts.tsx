import type {
  LibraryReleaseDetailResponse,
  LibraryReleaseTrackResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";

function positionNumber(track: LibraryReleaseTrackResponse) {
  return track.displayed_number.trim() || String(track.position);
}

function formatDuration(duration: number | null, locale: "en" | "ru") {
  if (duration === null)
    return locale === "ru" ? "Не указана" : "Not specified";
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

export function ReleaseMediaSummary({
  release,
}: {
  release: LibraryReleaseDetailResponse;
}) {
  const { locale } = useI18n();
  const copy =
    locale === "ru"
      ? {
          heading: "Носители и позиции",
          positionsByMedia: "Позиции по носителям",
          medium: "Носитель",
          title: "Название",
          format: "Формат",
          positions: "Позиции",
          untitled: "Без названия",
          unspecified: "Не указан",
          positionsUnspecified: "Позиции не указаны",
          mediaUnrecorded: "Сведения о носителях не сохранены.",
          releasePositions: "Позиции релиза:",
        }
      : {
          heading: "Media and positions",
          positionsByMedia: "Positions by media",
          medium: "Medium",
          title: "Title",
          format: "Format",
          positions: "Positions",
          untitled: "Untitled",
          unspecified: "Not specified",
          positionsUnspecified: "No positions specified",
          mediaUnrecorded: "Media information has not been saved.",
          releasePositions: "Release positions:",
        };
  const media = release.media ?? [];
  const tracks = release.tracks ?? [];
  return (
    <section
      className="library-release-media"
      aria-labelledby="release-media-heading"
    >
      <h2 id="release-media-heading">{copy.heading}</h2>
      {media.length ? (
        <section
          className="library-release-table-scroll"
          aria-label={copy.positionsByMedia}
        >
          <table>
            <thead>
              <tr>
                <th scope="col">{copy.medium}</th>
                <th scope="col">{copy.title}</th>
                <th scope="col">{copy.format}</th>
                <th scope="col">{copy.positions}</th>
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
                    <td>{medium.title || copy.untitled}</td>
                    <td>{medium.format || copy.unspecified}</td>
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
                        <span>{copy.positionsUnspecified}</span>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      ) : (
        <p>{copy.mediaUnrecorded}</p>
      )}
      {!media.length && tracks.length > 0 ? (
        <p>{copy.releasePositions}</p>
      ) : null}
    </section>
  );
}

export function PositionIdentity({
  track,
}: {
  track: LibraryReleaseTrackResponse;
}) {
  const { locale } = useI18n();
  const copy =
    locale === "ru"
      ? {
          position: "Позиция",
          title: "Название позиции",
          duration: "Длительность",
          composition: "Композиция",
          unspecified: "Не указано",
          facts: "Сведения о позиции",
        }
      : {
          position: "Position",
          title: "Position title",
          duration: "Duration",
          composition: "Recording",
          unspecified: "Not specified",
          facts: "Position details",
        };
  return (
    <dl
      className="library-release-position-facts"
      aria-label={`${copy.facts} ${positionNumber(track)}`}
    >
      <div>
        <dt>{copy.position}</dt>
        <dd>{positionNumber(track)}</dd>
      </div>
      <div>
        <dt>{copy.title}</dt>
        <dd>{track.title || copy.unspecified}</dd>
      </div>
      <div>
        <dt>{copy.duration}</dt>
        <dd>{formatDuration(track.duration_ms, locale)}</dd>
      </div>
      <div>
        <dt>{copy.composition}</dt>
        <dd>{track.recording_title || copy.unspecified}</dd>
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
