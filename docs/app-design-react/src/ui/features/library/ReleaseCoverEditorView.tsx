import type { RefObject } from "react";
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
  warning?: string;
  notice?: string;
  error?: string;
};

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
