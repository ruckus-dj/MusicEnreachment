import {
  LibraryPositionMetadataEditorOutlet,
  type LibraryPositionMetadataEditorSource,
} from "./LibraryPositionMetadataEditorOutlet";

export type TrackMetadataEditorsViewProps = {
  label?: string;
  recording?: LibraryPositionMetadataEditorSource;
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

const disclosure =
  "rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3";

/** Stable variant identity for keyed audio sources. */
function editorSourceKey(source: LibraryPositionMetadataEditorSource) {
  return "bind" in source ? source.key : undefined;
}

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
      {recording && <LibraryPositionMetadataEditorOutlet source={recording} />}
      {positionSummary ? (
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
      )}
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
