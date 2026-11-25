import type { ReactElement } from "react";

export type TrackMetadataEditorsViewProps = {
  label?: string;
  recording?: ReactElement;
  positionSummary?: string;
  position: ReactElement;
  audioLabel?: string;
  audioSummary?: string;
  noLinkedVariant?: string;
  selectedFileUnavailable?: string;
  selectedAudio?: ReactElement;
  otherAudioSummary?: string;
  otherAudio?: readonly ReactElement[];
};

const disclosure =
  "rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3";

export function TrackMetadataEditorsView({
  label,
  recording,
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
  return (
    <section className="grid min-w-0 gap-3" aria-label={label}>
      {recording}
      {positionSummary ? (
        <details className={disclosure}>
          <summary className="cursor-pointer font-semibold">
            {positionSummary}
          </summary>
          <div className="mt-3">{position}</div>
        </details>
      ) : (
        position
      )}
      {audioLabel && (
        <section className="min-w-0" aria-label={audioLabel}>
          <details className={disclosure}>
            <summary className="cursor-pointer font-semibold">
              {audioSummary}
            </summary>
            <div className="mt-3">
              {selectedAudio ?? (
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
                  <div className="mt-3 grid min-w-0 gap-3">{otherAudio}</div>
                </details>
              )}
            </div>
          </details>
        </section>
      )}
    </section>
  );
}
