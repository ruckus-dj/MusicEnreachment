import { type ReactNode, useId, useState } from "react";
import type { Locale } from "../../../i18n/types";
import {
  LibraryPositionMetadataEditorOutlet,
  type LibraryPositionMetadataEditorSource,
} from "./LibraryPositionMetadataEditorOutlet";

export type TrackMetadataEditorsViewProps = {
  locale?: Locale;
  label?: string;
  recording?: LibraryPositionMetadataEditorSource;
  recordingSupplement?: ReactNode;
  positionSupplement?: ReactNode;
  positionSummary?: string;
  position: LibraryPositionMetadataEditorSource;
  audioLabel?: string;
  audioSummary?: string;
  noLinkedVariant?: string;
  selectedFileUnavailable?: string;
  selectedAudio?: LibraryPositionMetadataEditorSource;
  otherAudioSummary?: string;
  otherAudio?: readonly LibraryPositionMetadataEditorSource[];
};

const scopeCopy = {
  en: {
    scope: "Editing scope",
    position: "Only this position in the edition",
    recording: "Recording across linked editions",
  },
  ru: {
    scope: "Область редактирования",
    position: "Только этот трек в издании",
    recording: "Запись во всех связанных изданиях",
  },
} as const;

const scopeClass =
  "flex min-w-0 flex-wrap items-center gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm";

const disclosure =
  "rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3";

/** Stable variant identity for keyed audio sources. */
function editorSourceKey(source: LibraryPositionMetadataEditorSource) {
  return "bind" in source ? source.key : undefined;
}

export function TrackMetadataEditorsView({
  locale = "en",
  label,
  recording,
  recordingSupplement,
  positionSupplement,
  positionSummary,
  position,
  audioLabel,
  audioSummary,
  noLinkedVariant,
  selectedFileUnavailable,
  selectedAudio,
  otherAudioSummary,
  otherAudio = [],
}: TrackMetadataEditorsViewProps) {
  const scopeText = scopeCopy[locale];
  const scopeName = useId();
  const [scope, setScope] = useState<"position" | "recording">("position");
  const positionEditor = positionSummary ? (
    <details className={disclosure}>
      <summary className="cursor-pointer font-semibold">
        {positionSummary}
      </summary>
      <div className="mt-3">
        <LibraryPositionMetadataEditorOutlet source={position} />
      </div>
    </details>
  ) : (
    <LibraryPositionMetadataEditorOutlet source={position} />
  );
  return (
    <section className="grid min-w-0 gap-3" aria-label={label}>
      {recording && (
        <fieldset className={scopeClass}>
          <legend className="text-xs text-[var(--muted)]">
            {scopeText.scope}
          </legend>
          <label className="flex cursor-pointer items-center gap-2">
            <input
              type="radio"
              name={scopeName}
              className="accent-[var(--accent)]"
              checked={scope === "position"}
              onChange={() => setScope("position")}
            />
            {scopeText.position}
          </label>
          <label className="flex cursor-pointer items-center gap-2">
            <input
              type="radio"
              name={scopeName}
              className="accent-[var(--accent)]"
              checked={scope === "recording"}
              onChange={() => setScope("recording")}
            />
            {scopeText.recording}
          </label>
        </fieldset>
      )}
      {/* Separate keyed containers keep each editor's draft state stable. */}
      <div
        key="position-editor"
        className={recording && scope !== "position" ? undefined : "grid gap-3"}
        hidden={Boolean(recording) && scope !== "position"}
      >
        {positionEditor}
        {positionSupplement}
      </div>
      {recording && (
        <div
          key="recording-editor"
          className={scope === "recording" ? "grid gap-3" : undefined}
          hidden={scope !== "recording"}
        >
          <LibraryPositionMetadataEditorOutlet source={recording} />
          {recordingSupplement}
        </div>
      )}
      {!recording && recordingSupplement}
      {audioLabel && (
        <section className="min-w-0" aria-label={audioLabel}>
          <details className={disclosure}>
            <summary className="cursor-pointer font-semibold">
              {audioSummary}
            </summary>
            <div className="mt-3">
              {selectedAudio ? (
                <LibraryPositionMetadataEditorOutlet
                  key={editorSourceKey(selectedAudio)}
                  source={selectedAudio}
                />
              ) : (
                <p className="m-0 text-sm text-[var(--muted)]">
                  {noLinkedVariant}
                </p>
              )}
              {selectedFileUnavailable && (
                <p className="m-0 text-sm text-[var(--muted)]">
                  {selectedFileUnavailable}
                </p>
              )}
              {otherAudio.length > 0 && (
                <details className="mt-3">
                  <summary className="cursor-pointer text-sm font-semibold">
                    {otherAudioSummary}
                  </summary>
                  <div className="mt-3 grid min-w-0 gap-3">
                    {otherAudio.map((source, index) => (
                      <LibraryPositionMetadataEditorOutlet
                        key={editorSourceKey(source) ?? index}
                        source={source}
                      />
                    ))}
                  </div>
                </details>
              )}
            </div>
          </details>
        </section>
      )}
    </section>
  );
}
