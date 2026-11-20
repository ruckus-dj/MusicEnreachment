import { useEffect, useId, useRef, useState } from "react";
import { Button } from "react-aria-components";
import { LocalRecordingArtistField } from "./LocalRecordingArtistField";
import {
  buildLocalRecordingCredits,
  type FindLocalArtists,
  formatLocalCredits,
  formatLocalDuration,
  isOutcomeUncertain,
  type LocalRecordingCreditSelection,
  type LocalRecordingDraft,
  type LocalReleaseArtist,
  type LocalReleaseRecording,
  parseLocalDuration,
  type SaveLocalArtist,
} from "./localReleaseFormModel";
import { localReleaseMessages } from "./localReleaseMessages";
import {
  errorClass,
  Field,
  LookupStatus,
  muted,
  panel,
  primary,
  row,
  rows,
  secondary,
  useLookup,
} from "./localReleaseUi";

export type LocalReleaseRecordingPickerProps = {
  locale: "ru" | "en";
  replacing?: boolean;
  findRecordings: (
    query: string,
    signal?: AbortSignal,
  ) => Promise<readonly LocalReleaseRecording[]>;
  saveRecording: (
    data: LocalRecordingDraft,
  ) => LocalReleaseRecording | Promise<LocalReleaseRecording>;
  findArtists: FindLocalArtists;
  saveArtist: SaveLocalArtist;
  savedArtists: readonly LocalReleaseArtist[];
  busy: boolean;
  onWritingChange: (pending: boolean) => void;
  onSaved: (recording: LocalReleaseRecording) => void;
  onArtistSaved: (artist: LocalReleaseArtist) => void;
  onAdd: (recording: LocalReleaseRecording) => void;
  onClose: () => void;
};

export function LocalReleaseRecordingPicker({
  locale,
  replacing = false,
  findRecordings,
  saveRecording,
  findArtists,
  saveArtist,
  savedArtists,
  busy,
  onWritingChange,
  onSaved,
  onArtistSaved,
  onAdd,
  onClose,
}: LocalReleaseRecordingPickerProps) {
  const copy = localReleaseMessages[locale];
  const choose = replacing ? copy.replaceRecording : copy.add;
  const lookup = useLookup<LocalReleaseRecording>();
  const [query, setQuery] = useState("");
  const [create, setCreate] = useState(false);
  const [duration, setDuration] = useState("");
  const [credits, setCredits] = useState<LocalRecordingCreditSelection[]>([]);
  const [invalid, setInvalid] = useState(false);
  const [saved, setSaved] = useState<LocalReleaseRecording>();
  const [saving, setSaving] = useState(false);
  const [artistSaving, setArtistSaving] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const savingRef = useRef(false);
  const titleRef = useRef<HTMLHeadingElement>(null);
  const descriptionBase = useId();
  useEffect(() => titleRef.current?.focus(), []);
  const trimQuery = query.trim();
  const isBusy = busy || saving || artistSaving;
  function clearSaveError() {
    if (saveError) setSaveError(false);
  }
  async function saveNewRecording() {
    if (savingRef.current || uncertain || artistSaving) return;
    if (!trimQuery || lookup.result.kind !== "ready" || !create) return;
    const durationMs = parseLocalDuration(duration);
    if (Number.isNaN(durationMs)) {
      setInvalid(true);
      return;
    }
    savingRef.current = true;
    setSaving(true);
    onWritingChange(true);
    setSaveError(false);
    try {
      const recording = await saveRecording({
        title: trimQuery,
        duration_ms: durationMs ?? undefined,
        ...(credits.length > 0
          ? {
              credits: buildLocalRecordingCredits(credits),
            }
          : {}),
      });
      onSaved(recording);
      setSaved(recording);
      setCreate(false);
    } catch (error) {
      if (isOutcomeUncertain(error)) setUncertain(true);
      else setSaveError(true);
    } finally {
      savingRef.current = false;
      setSaving(false);
      onWritingChange(false);
    }
  }
  const resultRow = (recording: LocalReleaseRecording, index: number) => {
    const descriptionId = `${descriptionBase}-recording-${index}`;
    const credits =
      recording.artistCredit ?? formatLocalCredits(recording.credits);
    return (
      <li key={recording.id} className={row}>
        <div className="min-w-0 wrap-anywhere">
          <strong>{recording.title || "—"}</strong>
          <p id={descriptionId} className={muted}>
            {[
              recording.provider,
              formatLocalDuration(recording.duration_ms),
              credits,
            ]
              .filter(Boolean)
              .join(" · ")}
          </p>
        </div>
        <Button
          className={secondary}
          aria-label={`${choose}: ${recording.title}`}
          aria-describedby={descriptionId}
          isDisabled={isBusy}
          onPress={() => onAdd(recording)}
        >
          {choose}
        </Button>
      </li>
    );
  };
  return (
    <section
      aria-labelledby="local-recording-picker"
      className={`${panel} grid gap-3`}
    >
      <div className={row}>
        <h3
          ref={titleRef}
          tabIndex={-1}
          id="local-recording-picker"
          className="m-0 text-sm font-semibold"
        >
          {replacing ? copy.replaceRecording : copy.addTrack}
        </h3>
        <Button className={secondary} isDisabled={isBusy} onPress={onClose}>
          {copy.closePicker}
        </Button>
      </div>
      <form
        aria-label={copy.recordingQuery}
        className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_auto]"
        onSubmit={(event) => {
          event.preventDefault();
          if (!trimQuery || lookup.result.kind === "pending" || isBusy) return;
          setSaved(undefined);
          setCreate(false);
          clearSaveError();
          void lookup.search((signal) => findRecordings(trimQuery, signal));
        }}
      >
        <Field
          label={copy.recordingQuery}
          value={query}
          required
          disabled={isBusy}
          onChange={(value) => {
            setQuery(value);
            lookup.reset();
            setCreate(false);
            setSaved(undefined);
            clearSaveError();
          }}
        />
        <Button
          className={secondary}
          type="submit"
          isDisabled={!trimQuery || lookup.result.kind === "pending" || isBusy}
        >
          {copy.search}
        </Button>
      </form>
      {saved ? (
        <ul className={rows} aria-label={copy.savedTracks}>
          {resultRow(saved, 0)}
        </ul>
      ) : (
        <LookupStatus
          result={lookup.result}
          copy={copy}
          empty={copy.noRecordings}
        >
          {(hits) => (
            <>
              {hits.length > 0 && (
                <ul className={rows} aria-label={copy.recordingResults}>
                  {hits.map((hit, index) => resultRow(hit, index))}
                </ul>
              )}
              {!create && (
                <Button
                  className={`${secondary} w-fit`}
                  isDisabled={isBusy}
                  onPress={() => {
                    setDuration("");
                    setCredits([]);
                    setInvalid(false);
                    clearSaveError();
                    setCreate(true);
                  }}
                >
                  {copy.createTrack}
                </Button>
              )}
            </>
          )}
        </LookupStatus>
      )}
      {create && lookup.result.kind === "ready" && (
        <section
          aria-label={copy.newRecording}
          className="grid gap-2 border-t border-[var(--line)] pt-3"
        >
          <h4 className="m-0 text-sm font-semibold">{copy.newRecording}</h4>
          <div className="grid gap-2 sm:grid-cols-2">
            <p className="m-0 self-center font-medium wrap-anywhere">
              {trimQuery}
            </p>
            <Field
              label={copy.duration}
              value={duration}
              disabled={isBusy}
              onChange={(value) => {
                setDuration(value);
                setInvalid(false);
                clearSaveError();
              }}
              placeholder="3:24"
            />
          </div>
          <LocalRecordingArtistField
            copy={copy}
            findArtists={findArtists}
            saveArtist={saveArtist}
            credits={credits}
            saved={savedArtists}
            busy={isBusy}
            onCreditsChange={setCredits}
            onSavingChange={(pending) => {
              setArtistSaving(pending);
              onWritingChange(pending);
            }}
            onSaved={onArtistSaved}
          />
          {invalid && (
            <p className={errorClass} role="alert">
              {copy.badDuration}
            </p>
          )}
          {saving && (
            <p className={muted} role="status">
              {copy.savingRecording}
            </p>
          )}
          {saveError && (
            <p className={errorClass} role="alert">
              {copy.saveRecordingError}
            </p>
          )}
          {uncertain && (
            <p className={errorClass} role="alert">
              {copy.saveRecordingUncertain}
            </p>
          )}
          <Button
            className={`${primary} w-fit`}
            isDisabled={isBusy || uncertain}
            onPress={() => void saveNewRecording()}
          >
            {copy.saveTrack}
          </Button>
        </section>
      )}
    </section>
  );
}
