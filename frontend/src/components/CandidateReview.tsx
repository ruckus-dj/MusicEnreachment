import { useEffect, useState } from "react";
import { api } from "../api/client";
import { parseRoute } from "../routing";
import type { Candidate, ProviderName } from "../types";

export function CandidateReview({
  provider = "musicbrainz",
  candidates,
  reason,
  disabled,
  onSelect,
}: {
  readonly provider?: ProviderName;
  readonly candidates: readonly Candidate[];
  readonly reason: string;
  readonly disabled: boolean;
  readonly onSelect: (selection: string) => void;
}) {
  const unique = [
    ...new Map(candidates.map((candidate) => [candidate.candidate_key, candidate])).values(),
  ].sort((left, right) => (right.evidence.score ?? 0) - (left.evidence.score ?? 0));
  const isAcoustId = provider === "acoustid";
  const route = parseRoute(window.location.pathname);
  const [decoded, setDecoded] = useState<
    Record<string, { readonly artist: string; readonly title: string; readonly album: string }>
  >({});
  const candidateKeys = unique.map((candidate) => candidate.candidate_key).join("|");
  useEffect(() => {
    if (!isAcoustId || !route.recordId || !route.sourceId) return;
    let cancelled = false;
    async function loadDecodedCandidates() {
      const results = await Promise.all(
        unique.map(async (candidate) => {
          if (candidate.evidence.artist && candidate.evidence.title && candidate.evidence.album)
            return [
              candidate.candidate_key,
              {
                artist: candidate.evidence.artist,
                title: candidate.evidence.title,
                album: candidate.evidence.album,
              },
            ] as const;
          try {
            const metadata = await api<{
              readonly artist: string;
              readonly title: string;
              readonly album: string;
            }>(
              `/api/library/records/${route.recordId}/sources/${route.sourceId}/candidates/${encodeURIComponent(candidate.candidate_key)}/musicbrainz`,
            );
            return [candidate.candidate_key, metadata] as const;
          } catch {
            return null;
          }
        }),
      );
      if (!cancelled)
        setDecoded(
          Object.fromEntries(
            results.filter(
              (
                result,
              ): result is readonly [
                string,
                { readonly artist: string; readonly title: string; readonly album: string },
              ] => result !== null,
            ),
          ),
        );
    }
    void loadDecodedCandidates();
    return () => {
      cancelled = true;
    };
  }, [candidateKeys, isAcoustId, route.recordId, route.sourceId]);
  return (
    <section className="candidate-review" aria-labelledby={`${provider}-candidate-title`}>
      <div className="section-heading">
        <div>
          <p className="eyebrow">{isAcoustId ? "AcousticID" : "MusicBrainz"}</p>
          <h3 id={`${provider}-candidate-title`}>
            {isAcoustId ? "Найденные записи" : "Варианты релиза"}
          </h3>
        </div>
        <span className="badge">{unique.length} вариантов</span>
      </div>
      <p className="candidate-reason">
        {reason ||
          (isAcoustId
            ? "Выберите запись, чтобы запросить её метаданные в MusicBrainz."
            : "Выберите подтверждённый релиз MusicBrainz.")}
      </p>
      {unique.length ? (
        <div className="candidate-list">
          {unique.map((candidate) => {
            const hasMetadata = Object.keys(candidate.evidence.tags).length > 0;
            const mbid = candidate.evidence.recording_mbid ?? candidate.candidate_key;
            const href = isAcoustId
              ? `https://musicbrainz.org/recording/${mbid}`
              : `https://musicbrainz.org/release/${candidate.candidate_key}`;
            const metadata = decoded[candidate.candidate_key];
            const title =
              metadata?.title ||
              candidate.evidence.title ||
              candidate.evidence.release ||
              (isAcoustId ? "MusicBrainz recording" : "Без названия релиза");
            const subtitle = isAcoustId
              ? [
                  metadata?.artist || candidate.evidence.artist,
                  metadata?.album || candidate.evidence.album,
                ]
                  .filter(Boolean)
                  .join(" · ") || candidate.candidate_key
              : candidate.evidence.artist || "Исполнитель не указан";
            return (
              <article className="candidate-card" key={`${provider}-${candidate.candidate_key}`}>
                <div>
                  <strong>{title}</strong>
                  <small>{subtitle}</small>
                  <a href={href} target="_blank" rel="noreferrer">
                    Открыть в MusicBrainz
                  </a>
                  {!hasMetadata && !isAcoustId && (
                    <small>Метаданные отсутствуют; повторите запрос</small>
                  )}
                </div>
                <div className="candidate-score">
                  {candidate.evidence.score === null
                    ? "—"
                    : `${Math.round(candidate.evidence.score * 100)}%`}
                  <small>оценка</small>
                </div>
                <button
                  type="button"
                  className="primary"
                  disabled={disabled || (!isAcoustId && !hasMetadata)}
                  onClick={() => onSelect(`${provider}:${candidate.candidate_key}`)}
                >
                  {isAcoustId
                    ? "Выбрать запись"
                    : hasMetadata
                      ? "Выбрать и подтвердить"
                      : "Нет метаданных"}
                </button>
              </article>
            );
          })}
        </div>
      ) : (
        <div className="candidate-empty">
          Провайдер не вернул вариантов. Повторите запрос после восстановления связи.
        </div>
      )}
    </section>
  );
}
