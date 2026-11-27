import { type RefObject, useId, useState } from "react";
import { AppIcon } from "../../components/AppIcon";
import type { Locale } from "../../i18n/types";
import {
  releaseControlAlertClass,
  releaseControlButtonClass,
} from "./libraryReleaseControlStyles";
import { releaseCoverEditorCopy } from "./releaseCoverEditorCopy";

export type ReleaseCoverEditorImage = {
  imageSrc?: string;
  checksum?: string;
};

export type ReleaseCoverCandidateChoice = {
  id: string;
  front: boolean;
  back: boolean;
  types: readonly string[];
  comment: string;
  thumbnailSrc: string;
};

export type ReleaseCoverCandidatesModel = {
  status: "idle" | "loading" | "pending" | "loaded" | "error";
  items: readonly ReleaseCoverCandidateChoice[];
  error?: string;
  selectingId?: string;
  disabled?: boolean;
  onReveal: () => void;
  onRetry: () => void;
  onSelect: (id: string) => void;
};

export type ReleaseCoverEditorViewProps = {
  locale: Locale;
  releaseId: string;
  selected?: ReleaseCoverEditorImage;
  pending?: ReleaseCoverEditorImage;
  fileInput: RefObject<HTMLInputElement | null>;
  busy: boolean;
  savePendingDisabled: boolean;
  applyDisabled: boolean;
  retryDisabled: boolean;
  onFileChange: (file?: File) => void;
  onSavePending: () => void;
  onApply: () => void;
  onRetry: () => void;
  candidates?: ReleaseCoverCandidatesModel;
  warning?: string;
  notice?: string;
  error?: string;
};

type CoverEditorCopy = (typeof releaseCoverEditorCopy)[Locale];

const candidateTypeCopyKeys: Record<string, keyof CoverEditorCopy> = {
  front: "candidateFront",
  back: "candidateBack",
  booklet: "candidateBooklet",
  medium: "candidateMedium",
  tray: "candidateTray",
  spine: "candidateSpine",
  sticker: "candidateSticker",
  poster: "candidatePoster",
  other: "candidateOther",
};

function candidateTypeLabel(type: string, text: CoverEditorCopy): string {
  const key = candidateTypeCopyKeys[type.trim().toLocaleLowerCase()];
  return key ? text[key] : type.trim();
}

function candidateChips(
  candidate: ReleaseCoverCandidateChoice,
  text: CoverEditorCopy,
): string[] {
  const chips: string[] = [];
  const add = (label: string) => {
    const value = label.trim();
    if (!value) return;
    const normalized = value.toLocaleLowerCase();
    if (chips.some((chip) => chip.toLocaleLowerCase() === normalized)) return;
    chips.push(value);
  };
  if (candidate.front) add(text.candidateFront);
  if (candidate.back) add(text.candidateBack);
  for (const type of candidate.types) add(candidateTypeLabel(type, text));
  return chips;
}

function candidateLabel(
  candidate: ReleaseCoverCandidateChoice,
  text: CoverEditorCopy,
): string {
  const details = [...candidateChips(candidate, text), candidate.comment.trim()]
    .filter(Boolean)
    .join(", ");
  return details
    ? `${text.candidateSelect}: ${details}`
    : `${text.candidateSelect}: ${text.candidateArtwork}`;
}

function CoverCandidateList({
  candidates,
  locale,
}: {
  candidates: ReleaseCoverCandidatesModel;
  locale: Locale;
}) {
  const text = releaseCoverEditorCopy[locale];
  if (candidates.status === "idle" || candidates.status === "loading")
    return <p role="status">{text.candidatesLoading}</p>;
  if (candidates.status === "pending")
    return (
      <div className="grid justify-items-start gap-2">
        <p role="status">{text.candidatesPending}</p>
        <button
          type="button"
          className={releaseControlButtonClass}
          onClick={candidates.onRetry}
        >
          {text.candidatesRetry}
        </button>
      </div>
    );
  if (candidates.status === "error")
    return (
      <div className="grid justify-items-start gap-2">
        <p role="alert" className={releaseControlAlertClass}>
          {candidates.error}
        </p>
        <button
          type="button"
          className={releaseControlButtonClass}
          onClick={candidates.onRetry}
        >
          {text.candidatesRetry}
        </button>
      </div>
    );
  if (!candidates.items.length)
    return <p className="text-[var(--muted)]">{text.candidatesEmpty}</p>;
  return (
    <ul
      className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(min(100%,240px),1fr))] gap-3 p-0"
      aria-busy={candidates.selectingId !== undefined || undefined}
    >
      {candidates.items.map((candidate) => {
        const chips = candidateChips(candidate, text);
        const selecting = candidates.selectingId === candidate.id;
        return (
          <li key={candidate.id} className="min-w-0">
            <button
              type="button"
              className="flex min-h-9 w-full min-w-0 items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2 text-left text-[var(--ink)] hover:enabled:border-[var(--control)] hover:enabled:bg-[var(--raised)] disabled:cursor-default disabled:opacity-60 focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2"
              aria-label={candidateLabel(candidate, text)}
              aria-busy={selecting || undefined}
              disabled={Boolean(candidates.disabled || candidates.selectingId)}
              onClick={() => candidates.onSelect(candidate.id)}
            >
              <img
                className="size-20 flex-none rounded-md border border-[var(--line)] bg-[var(--raised)] object-contain"
                src={candidate.thumbnailSrc}
                alt=""
                loading="lazy"
                decoding="async"
                referrerPolicy="no-referrer"
              />
              <span className="grid min-w-0 gap-1.5">
                <span className="flex flex-wrap gap-1">
                  {chips.length ? (
                    chips.map((chip) => (
                      <span
                        key={chip}
                        className="inline-flex min-h-6 items-center rounded-full border border-[var(--line)] bg-[var(--raised)] px-2 py-0.5 text-xs text-[var(--muted)]"
                      >
                        {chip}
                      </span>
                    ))
                  ) : (
                    <span className="inline-flex min-h-6 items-center rounded-full border border-[var(--line)] bg-[var(--raised)] px-2 py-0.5 text-xs text-[var(--muted)]">
                      {text.candidateArtwork}
                    </span>
                  )}
                </span>
                {candidate.comment.trim() && (
                  <span className="text-sm [overflow-wrap:anywhere]">
                    {candidate.comment.trim()}
                  </span>
                )}
                {selecting && (
                  <span className="text-xs text-[var(--muted)]">
                    {text.candidateSelecting}
                  </span>
                )}
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}

function CoverCandidateDisclosure({
  candidates,
  locale,
}: {
  candidates: ReleaseCoverCandidatesModel;
  locale: Locale;
}) {
  const [open, setOpen] = useState(false);
  const contentID = useId();
  const text = releaseCoverEditorCopy[locale];
  return (
    <section className="min-w-0 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
      <button
        type="button"
        className="flex min-h-9 w-full items-center justify-between gap-2 text-left text-sm font-semibold text-[var(--accent-strong)] hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2"
        aria-expanded={open}
        aria-controls={contentID}
        onClick={() => {
          const next = !open;
          setOpen(next);
          if (next) candidates.onReveal();
        }}
      >
        {text.candidatesSummary}
        <AppIcon
          name="chevronDown"
          className={`size-4 flex-none transition-transform ${open ? "rotate-180" : ""}`}
        />
      </button>
      {open && (
        <div id={contentID} className="mt-3">
          <CoverCandidateList candidates={candidates} locale={locale} />
        </div>
      )}
    </section>
  );
}

export function ReleaseCoverEditorView({
  locale,
  releaseId,
  selected,
  pending,
  fileInput,
  busy,
  savePendingDisabled,
  applyDisabled,
  retryDisabled,
  onFileChange,
  onSavePending,
  onApply,
  onRetry,
  candidates,
  warning,
  notice,
  error,
}: ReleaseCoverEditorViewProps) {
  const text = releaseCoverEditorCopy[locale];
  return (
    <section
      aria-labelledby="release-cover-title"
      className="space-y-3 rounded border border-[var(--line)] bg-surface p-4"
    >
      <h2
        id="release-cover-title"
        className="text-base leading-[22px] font-semibold"
      >
        {text.title}
      </h2>
      {selected ? (
        <div>
          <p>{text.selectedCover}</p>
          <img
            className="max-h-48"
            alt={text.selectedCoverAlt}
            src={selected.imageSrc}
          />
          <p className="[overflow-wrap:anywhere]">
            SHA-256: {selected.checksum ?? text.unspecified}
          </p>
        </div>
      ) : (
        <p>{text.noSelectedCover}</p>
      )}
      {pending && (
        <div>
          <p>{text.pendingCover}</p>
          <img
            className="max-h-48"
            alt={text.pendingCoverAlt}
            src={pending.imageSrc}
          />
          <p className="[overflow-wrap:anywhere]">
            SHA-256: {pending.checksum ?? text.unspecified}
          </p>
        </div>
      )}
      {candidates && (
        <CoverCandidateDisclosure
          key={releaseId}
          candidates={candidates}
          locale={locale}
        />
      )}
      <label className="block">
        {text.choosePngJpeg}
        <input
          key={releaseId}
          ref={fileInput}
          type="file"
          className="max-w-full rounded bg-raised text-ink max-[599px]:min-h-10"
          accept="image/png,image/jpeg"
          disabled={busy}
          onChange={(event) => onFileChange(event.target.files?.[0])}
        />
      </label>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={savePendingDisabled}
        onClick={onSavePending}
      >
        {text.savePendingCover}
      </button>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={applyDisabled}
        onClick={onApply}
      >
        {text.applyCoverToTracks}
      </button>
      <button
        type="button"
        className={releaseControlButtonClass}
        disabled={retryDisabled}
        onClick={onRetry}
      >
        {text.retrySelectedCover}
      </button>
      {warning && (
        <p role="alert" className={releaseControlAlertClass}>
          {warning}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {error && (
        <p role="alert" className={releaseControlAlertClass}>
          {error}
        </p>
      )}
    </section>
  );
}
