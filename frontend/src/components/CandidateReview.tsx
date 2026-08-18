import { useEffect, useState } from "react";
import { api } from "../api/client";
import { parseRoute } from "../routing";
import type { Candidate } from "../types";

export function CandidateReview({
  entity,
  candidates,
  reason,
  disabled,
  musicbrainzHost,
  selectedKey,
  compatibleWith,
  onSelect,
}: {
  readonly entity: "recording" | "release";
  readonly candidates: readonly Candidate[];
  readonly reason: string;
  readonly disabled: boolean;
  readonly musicbrainzHost: string | null;
  readonly selectedKey: string | null;
  readonly compatibleWith: string | null;
  readonly onSelect: (selection: string) => void;
}) {
  const unique = [
    ...new Map(
      candidates.map((candidate) => [`${entity}-${candidate.candidate_key}`, candidate]),
    ).values(),
  ].sort((left, right) => (right.evidence.score ?? 0) - (left.evidence.score ?? 0));
  const route = parseRoute(window.location.pathname);
  const [decoded, setDecoded] = useState<
    Record<string, { readonly artist: string; readonly title: string; readonly album: string }>
  >({});
  const candidateKeys = unique.map((candidate) => candidate.candidate_key).join("|");
  const selectedCandidate = unique.find(
    (candidate) =>
      candidate.candidate_key === selectedKey || candidate.evidence.recording_mbid === selectedKey,
  );
  const selectedMetadata = selectedCandidate ? decoded[selectedCandidate.candidate_key] : undefined;
  const selectedTitle =
    selectedMetadata?.title ||
    selectedCandidate?.evidence.title ||
    selectedCandidate?.evidence.release ||
    (selectedKey
      ? `${entity === "recording" ? "Запись" : "Релиз"} ${selectedKey}`
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
            {entity === "recording" ? "Recording MBID" : "Release MBID"}
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
                ? "Выберите запись MusicBrainz для продолжения."
                : "Выберите подтверждённый релиз MusicBrainz.")}
          </p>
          {unique.length ? (
            <div className="candidate-list">
              {unique.map((candidate) => {
                const hasMetadata = Object.keys(candidate.evidence.tags).length > 0;
                const candidateEntity = candidate.evidence.entity ?? entity;
                const candidateIsAcoustId = candidate.evidence.provider === "acoustid";
                const acoustidScore =
                  candidate.evidence.acoustid_score ??
                  (candidateIsAcoustId ? candidate.evidence.score : null);
                const musicbrainzScore =
                  candidate.evidence.musicbrainz_score ??
                  (!candidateIsAcoustId ? candidate.evidence.score : null);
                const compatible =
                  compatibleWith === null ||
                  (candidate.evidence.compatible_ids?.length ?? 0) === 0 ||
                  candidate.evidence.compatible_ids?.includes(compatibleWith) === true;
                const candidateMbid = candidate.candidate_key;
                const selected =
                  selectedKey !== null &&
                  (candidateMbid === selectedKey ||
                    candidate.evidence.recording_mbid === selectedKey);
                const related =
                  !selected &&
                  compatibleWith !== null &&
                  candidate.evidence.compatible_ids?.includes(compatibleWith) === true;
                const linkedMbid =
                  candidateEntity === "recording"
                    ? (candidate.evidence.recording_mbid ?? candidateMbid)
                    : candidateMbid;
                const musicbrainzBase = musicbrainzHost?.replace(/\/$/, "");
                const candidateHref = musicbrainzBase
                  ? `${musicbrainzBase}/${candidateEntity}/${linkedMbid}`
                  : null;
                const linkedRecordingMbid = candidate.evidence.recording_mbid;
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
                      {acoustidScore != null && (
                        <small>AcousticID: {Math.round(acoustidScore * 100)}%</small>
                      )}
                      {musicbrainzScore != null && (
                        <small>MusicBrainz: {Math.round(musicbrainzScore * 100)}%</small>
                      )}
                      <small>
                        {selected
                          ? "Выбрано"
                          : related
                            ? "Связано с выбранным вариантом"
                            : compatible
                              ? "Совместимо"
                              : "Несовместимо: выбор сбросит второй вариант"}
                      </small>
                      {candidateHref && (
                        <a href={candidateHref} target="_blank" rel="noreferrer">
                          {candidateEntity === "recording" ? "Запись" : "Релиз"}: {linkedMbid}
                        </a>
                      )}
                      {candidateEntity === "release" && linkedRecordingMbid && (
                        <a
                          href={`${musicbrainzBase}/recording/${linkedRecordingMbid}`}
                          target="_blank"
                          rel="noreferrer"
                        >
                          Запись: {linkedRecordingMbid}
                        </a>
                      )}
                      {!hasMetadata && !candidateIsAcoustId && (
                        <small>Метаданные отсутствуют; повторите запрос</small>
                      )}
                    </div>
                    <div className="candidate-score">
                      {candidate.evidence.score === null
                        ? "—"
                        : `${Math.round(candidate.evidence.score * 100)}%`}
                      <small>приоритетная оценка</small>
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
                          ? `Выбрать запись ${candidateMbid}, оценка ${candidate.evidence.score === null ? "неизвестна" : `${Math.round(candidate.evidence.score * 100)}%`}`
                          : `Выбрать релиз ${candidateMbid}, оценка ${candidate.evidence.score === null ? "неизвестна" : `${Math.round(candidate.evidence.score * 100)}%`}`
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
