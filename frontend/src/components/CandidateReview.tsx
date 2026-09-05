import { useEffect, useState } from "react";
import { api } from "../api/client";
import { parseRoute } from "../routing";
import type { Candidate, Tags } from "../types";

const COMPARISON_FIELDS = [
  "TITLE",
  "ARTIST",
  "ALBUM",
  "ALBUMARTIST",
  "DATE",
  "TRACKNUMBER",
  "TRACKTOTAL",
  "DISCNUMBER",
  "DISCTOTAL",
] as const;

function formatDuration(seconds: number | null | undefined): string {
  if (seconds === null || seconds === undefined) return "—";
  const rounded = Math.round(seconds);
  return `${Math.floor(rounded / 60)}:${String(rounded % 60).padStart(2, "0")}`;
}

function formatScoreFactor(
  raw: number | null | undefined,
  contribution: number | null | undefined,
): string | null {
  if (raw === null || raw === undefined || contribution === null || contribution === undefined)
    return null;
  return `${(raw * 100).toFixed(2)}% (${(contribution * 100).toFixed(2)}%)`;
}

function compositeScore(candidate: Candidate): number | null {
  return candidate.evidence.score_components === null ||
    candidate.evidence.score_components === undefined
    ? null
    : candidate.evidence.score;
}

export function CandidateReview({
  entity,
  candidates,
  reason,
  disabled,
  musicbrainzHost,
  selectedKey,
  compatibleWith,
  sourceTags,
  sourceDurationSeconds,
  onSelect,
}: {
  readonly entity: "recording" | "recording_release";
  readonly candidates: readonly Candidate[];
  readonly reason: string;
  readonly disabled: boolean;
  readonly musicbrainzHost: string | null;
  readonly selectedKey: string | null;
  readonly compatibleWith: string | null;
  readonly sourceTags: Tags;
  readonly sourceDurationSeconds: number | null;
  readonly onSelect: (selection: string) => void;
}) {
  const unique = [
    ...new Map(
      candidates.map((candidate) => [`${entity}-${candidate.candidate_key}`, candidate]),
    ).values(),
  ].sort((left, right) => {
    const leftScore = compositeScore(left);
    const rightScore = compositeScore(right);
    return (
      Number(rightScore === null) - Number(leftScore === null) ||
      (rightScore ?? -1) - (leftScore ?? -1)
    );
  });
  const route = parseRoute(window.location.pathname, window.location.search);
  const [decoded, setDecoded] = useState<
    Record<string, { readonly artist: string; readonly title: string; readonly album: string }>
  >({});
  const candidateKeys = unique.map((candidate) => candidate.candidate_key).join("|");
  const selectedCandidate = unique.find(
    (candidate) =>
      candidate.candidate_key === selectedKey ||
      candidate.evidence.recording_mbid === selectedKey ||
      candidate.evidence.release_mbid === selectedKey,
  );
  const selectedMetadata = selectedCandidate ? decoded[selectedCandidate.candidate_key] : undefined;
  const selectedTitle =
    selectedMetadata?.title ||
    selectedCandidate?.evidence.title ||
    selectedCandidate?.evidence.release ||
    (selectedKey
      ? `${entity === "recording" ? "Запись" : "Запись и релиз"} ${selectedKey}`
      : "Вариант не выбран");
  const selectedSubtitle = selectedCandidate
    ? [
        selectedMetadata?.artist || selectedCandidate.evidence.artist,
        selectedMetadata?.album || selectedCandidate.evidence.album,
      ]
        .filter(Boolean)
        .join(" · ")
    : selectedKey
      ? "Подтверждённый вариант"
      : "Нужно выбрать вариант для продолжения";
  const [isOpen, setIsOpen] = useState(!selectedKey && unique.length > 0);
  useEffect(() => {
    setIsOpen(!selectedKey && unique.length > 0);
  }, [selectedKey, candidateKeys]);
  useEffect(() => {
    if (!route.recordId || !route.sourceId) return;
    let cancelled = false;
    async function loadDecodedCandidates() {
      const results = await Promise.all(
        unique.map(async (candidate) => {
          if (
            candidate.evidence.provider !== "acoustid" ||
            (candidate.evidence.artist && candidate.evidence.title && candidate.evidence.album)
          )
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
  }, [candidateKeys, route.recordId, route.sourceId]);
  return (
    <section className="candidate-review" aria-labelledby={`${entity}-candidate-title`}>
      <button
        type="button"
        className={`candidate-disclosure ${selectedKey ? "selected" : "needs-selection"}`}
        aria-expanded={isOpen}
        aria-controls={`${entity}-candidate-options`}
        onClick={() => setIsOpen((open) => !open)}
      >
        <span className="candidate-disclosure-copy">
          <span className="eyebrow">
            {entity === "recording" ? "Recording MBID" : "Recording + Release"}
          </span>
          <strong id={`${entity}-candidate-title`}>{selectedTitle}</strong>
          <small>{selectedSubtitle}</small>
        </span>
        <span className="candidate-disclosure-meta">
          <span className={`candidate-state ${selectedKey ? "confirmed" : "attention"}`}>
            {selectedKey ? "Выбрано" : "Нужно выбрать"}
          </span>
          <span className="candidate-chevron" aria-hidden="true">
            {isOpen ? "⌃" : "⌄"}
          </span>
        </span>
      </button>
      {isOpen && (
        <div id={`${entity}-candidate-options`} className="candidate-options">
          <p className="candidate-reason">
            {reason ||
              (entity === "recording"
                ? "Выберите запись AcousticID для продолжения."
                : "Выберите единого кандидата записи и релиза MusicBrainz.")}
          </p>
          {unique.length ? (
            <div className="candidate-list">
              {unique.map((candidate) => {
                const hasMetadata = Object.keys(candidate.evidence.tags).length > 0;
                const candidateEntity = candidate.evidence.entity ?? entity;
                const candidateIsAcoustId = candidate.evidence.provider === "acoustid";
                const acoustidScore = candidate.evidence.acoustid_score;
                const musicbrainzScore = candidate.evidence.musicbrainz_score;
                const score = compositeScore(candidate);
                const trackNumber = candidate.evidence.tags.TRACKNUMBER?.split("/", 1)[0];
                const trackTotal = candidate.evidence.tags.TRACKTOTAL?.split("/", 1)[0];
                const discNumber = candidate.evidence.tags.DISCNUMBER?.split("/", 1)[0];
                const discTotal = candidate.evidence.tags.DISCTOTAL?.split("/", 1)[0];
                const position = trackNumber
                  ? `${discNumber ? `${discNumber}/${discTotal ?? "?"} · ` : ""}${trackNumber}${trackTotal ? `/${trackTotal}` : ""}`
                  : null;
                const components = candidate.evidence.score_components;
                const fieldScores = {
                  TITLE: formatScoreFactor(components?.title_match, components?.title),
                  ARTIST: formatScoreFactor(
                    components?.recording_artist_match,
                    components?.recording_artist,
                  ),
                  ALBUM: formatScoreFactor(components?.release_match, components?.release),
                  ALBUMARTIST: formatScoreFactor(
                    components?.release_artist_match,
                    components?.release_artist,
                  ),
                  DATE: null,
                  TRACKNUMBER: formatScoreFactor(
                    components?.track_number_match,
                    components?.track_number,
                  ),
                  TRACKTOTAL: formatScoreFactor(
                    components?.track_total_match,
                    components?.track_total,
                  ),
                  DISCNUMBER: formatScoreFactor(
                    components?.disc_number_match,
                    components?.disc_number,
                  ),
                  DISCTOTAL: formatScoreFactor(
                    components?.disc_total_match,
                    components?.disc_total,
                  ),
                  DURATION: formatScoreFactor(components?.duration_match, components?.duration),
                } satisfies Record<string, string | null>;
                const compatible =
                  compatibleWith === null ||
                  (candidate.evidence.compatible_ids?.length ?? 0) === 0 ||
                  candidate.evidence.compatible_ids?.includes(compatibleWith) === true;
                const candidateMbid = candidate.candidate_key;
                const releaseMbid =
                  candidate.evidence.release_mbid ??
                  (candidateEntity === "recording_release" ? candidateMbid.split(":", 1)[0] : null);
                const selected =
                  selectedKey !== null &&
                  (candidateMbid === selectedKey ||
                    candidate.evidence.recording_mbid === selectedKey ||
                    candidate.evidence.release_mbid === selectedKey);
                const related =
                  !selected &&
                  compatibleWith !== null &&
                  candidate.evidence.compatible_ids?.includes(compatibleWith) === true;
                const musicbrainzBase = musicbrainzHost?.replace(/\/$/, "");
                const candidateTags = candidate.evidence.tags;
                const candidateValues: Record<string, string> = {
                  TITLE: candidateTags.TITLE || candidate.evidence.title || "—",
                  ARTIST: candidateTags.ARTIST || candidate.evidence.artist || "—",
                  ALBUM:
                    candidateTags.ALBUM ||
                    candidate.evidence.album ||
                    candidate.evidence.release ||
                    "—",
                  ALBUMARTIST: candidateTags.ALBUMARTIST || "—",
                  DATE: candidateTags.DATE || "—",
                  TRACKNUMBER: candidateTags.TRACKNUMBER || "—",
                  TRACKTOTAL: candidateTags.TRACKTOTAL || "—",
                  DISCNUMBER: candidateTags.DISCNUMBER || "—",
                  DISCTOTAL: candidateTags.DISCTOTAL || "—",
                };
                const metadata = decoded[candidate.candidate_key];
                const title =
                  metadata?.title ||
                  candidate.evidence.title ||
                  candidate.evidence.release ||
                  (candidateEntity === "recording"
                    ? "MusicBrainz recording"
                    : "Без названия релиза");
                const subtitle = candidateIsAcoustId
                  ? [
                      metadata?.artist || candidate.evidence.artist,
                      metadata?.album || candidate.evidence.album,
                    ]
                      .filter(Boolean)
                      .join(" · ") || candidate.candidate_key
                  : candidate.evidence.artist || "Исполнитель не указан";
                return (
                  <article
                    className={`candidate-card ${selected ? "candidate-card-selected" : ""} ${related ? "candidate-card-related" : ""}`}
                    aria-label={`${candidateEntity === "recording" ? "Запись" : "Релиз"}: ${title}`}
                    key={`${candidate.evidence.provider}-${candidateEntity}-${candidate.candidate_key}`}
                  >
                    <div>
                      <strong>{title}</strong>
                      <small>{subtitle}</small>
                      {candidateEntity === "recording_release" && position && (
                        <small>Позиция: {position}</small>
                      )}
                      {candidateEntity === "recording_release" &&
                        candidate.evidence.disambiguation && (
                          <small>Приписка MusicBrainz: {candidate.evidence.disambiguation}</small>
                        )}
                      <dl className="candidate-comparison">
                        {COMPARISON_FIELDS.map((field) => (
                          <div key={field}>
                            <dt>{field}</dt>
                            <dd>
                              <span>
                                <b>Источник:</b> {sourceTags[field] || "—"}
                                {fieldScores[field] && <em>{fieldScores[field]}</em>}
                              </span>
                              <span>
                                <b>Кандидат:</b> {candidateValues[field]}
                              </span>
                            </dd>
                          </div>
                        ))}
                        <div>
                          <dt>DURATION</dt>
                          <dd>
                            <span>
                              <b>Источник:</b> {formatDuration(sourceDurationSeconds)}
                              {fieldScores.DURATION && <em>{fieldScores.DURATION}</em>}
                            </span>
                            <span>
                              <b>Кандидат:</b> {formatDuration(candidate.evidence.duration_seconds)}
                            </span>
                          </dd>
                        </div>
                      </dl>
                      <dl className="candidate-identities">
                        {releaseMbid && (
                          <div>
                            <dt>Релиз</dt>
                            <dd>
                              {musicbrainzBase ? (
                                <a
                                  href={`${musicbrainzBase}/release/${releaseMbid}`}
                                  target="_blank"
                                  rel="noreferrer"
                                >
                                  {releaseMbid}
                                </a>
                              ) : (
                                releaseMbid
                              )}
                            </dd>
                          </div>
                        )}
                        {candidate.evidence.recording_mbid && (
                          <div>
                            <dt>Запись</dt>
                            <dd>
                              {musicbrainzBase ? (
                                <a
                                  href={`${musicbrainzBase}/recording/${candidate.evidence.recording_mbid}`}
                                  target="_blank"
                                  rel="noreferrer"
                                >
                                  {candidate.evidence.recording_mbid}
                                </a>
                              ) : (
                                candidate.evidence.recording_mbid
                              )}
                            </dd>
                          </div>
                        )}
                      </dl>
                      {acoustidScore != null && (
                        <small>AcousticID: {Math.round(acoustidScore * 100)}%</small>
                      )}
                      {musicbrainzScore != null && (
                        <small>MusicBrainz: {Math.round(musicbrainzScore * 100)}%</small>
                      )}
                      {!compatible && <small>Несовместимо: выбор сбросит второй вариант</small>}
                      {!hasMetadata && !candidateIsAcoustId && (
                        <small>Метаданные отсутствуют; повторите запрос</small>
                      )}
                    </div>
                    <div className="candidate-score">
                      {score === null ? "—" : `${(score * 100).toFixed(2)}%`}
                      <small>Наш скоринг</small>
                    </div>
                    <button
                      type="button"
                      className="primary"
                      disabled={disabled || (!candidateIsAcoustId && !hasMetadata)}
                      onClick={() =>
                        onSelect(
                          `${candidate.evidence.provider}:${candidateEntity}:${candidateMbid}`,
                        )
                      }
                      aria-label={
                        candidateEntity === "recording"
                          ? `Выбрать запись ${candidateMbid}, наш скоринг ${score === null ? "неизвестен" : `${Math.round(score * 100)}%`}`
                          : `Выбрать релиз ${candidateMbid}, наш скоринг ${score === null ? "неизвестен" : `${Math.round(score * 100)}%`}`
                      }
                    >
                      {candidateEntity === "recording"
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
        </div>
      )}
    </section>
  );
}
