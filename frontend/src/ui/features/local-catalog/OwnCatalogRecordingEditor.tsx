import { type ComponentType, useRef, useState } from "react";
import type { Locale } from "../../i18n/types";
import type {
  GenrePickerCatalog,
  GenreReference,
} from "../library/GenrePicker";
import { LocalRecordingArtistField } from "../local-release/LocalRecordingArtistField";
import {
  LocalReleaseGenreCatalogOutlet,
  type LocalReleaseGenreCatalogProviderProps,
  type LocalReleaseGenreSelectionAdapter,
} from "../local-release/LocalReleaseGenreCatalogOutlet";
import {
  type FindLocalArtists,
  isOutcomeUncertain,
  type LocalRecordingCreditSelection,
  type LocalReleaseArtist,
  parseLocalDuration,
  type SaveLocalArtist,
} from "../local-release/localReleaseFormModel";
import { localReleaseMessages } from "../local-release/localReleaseMessages";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import { OwnCatalogRecordEditorView } from "./OwnCatalogRecordEditorView";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";

/** Portable recording draft; the host adapts the record and wire trimming. */
export type OwnCatalogRecordingDraft = {
  title: string;
  durationInput: string;
  tags: string;
  genres: GenreReference[];
  credits: LocalRecordingCreditSelection[];
};

/**
 * Portable Own Catalog recording editor. The host supplies only the initial
 * draft, artist lookup actions, typed genre catalog lifetime and save/back
 * actions; the portable tree owns the shell, fields, ordered credits, nested
 * genre binder and guarded submit lifecycle.
 */
export function OwnCatalogRecordingEditor({
  locale,
  initialValues,
  findArtists,
  saveArtist,
  genreCatalogProvider,
  genreCatalogModel,
  genreCatalogSelection,
  onSave,
  onBack,
}: {
  locale: Locale;
  /** Initial draft projection; absent for a new recording. */
  initialValues?: OwnCatalogRecordingDraft;
  findArtists: FindLocalArtists;
  saveArtist: SaveLocalArtist;
  /**
   * Stable host provider mounted with this editor opening. Live hosts keep the
   * original catalog hook inside it so a new opening starts with fresh state.
   */
  genreCatalogProvider: ComponentType<LocalReleaseGenreCatalogProviderProps>;
  /** Demo hosts may bind an already-built catalog model instead of a hook. */
  genreCatalogModel?: GenrePickerCatalog;
  /** Demo-only projection for stored draft rows; live leaves this unset. */
  genreCatalogSelection?: LocalReleaseGenreSelectionAdapter;
  onSave: (draft: OwnCatalogRecordingDraft) => void | Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const copy = localReleaseMessages[locale];
  const GenreCatalogProvider = genreCatalogProvider;
  const [title, setTitle] = useState(initialValues?.title ?? "");
  const [duration, setDuration] = useState(initialValues?.durationInput ?? "");
  const [tags, setTags] = useState(initialValues?.tags ?? "");
  const [genres, setGenres] = useState<GenreReference[]>(() => [
    ...(initialValues?.genres ?? []),
  ]);
  const [credits, setCredits] = useState<LocalRecordingCreditSelection[]>(
    () => [...(initialValues?.credits ?? [])],
  );
  const [savedArtists, setSavedArtists] = useState<LocalReleaseArtist[]>([]);
  const [badDuration, setBadDuration] = useState(false);
  const [artistSaving, setArtistSaving] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const savingRef = useRef(false);

  async function save() {
    if (savingRef.current || uncertain || artistSaving || !title.trim()) return;
    if (Number.isNaN(parseLocalDuration(duration))) {
      setBadDuration(true);
      return;
    }
    setBadDuration(false);
    savingRef.current = true;
    setSaving(true);
    setSaveError(false);
    try {
      await onSave({ title, durationInput: duration, tags, genres, credits });
    } catch (error) {
      if (isOutcomeUncertain(error)) setUncertain(true);
      else setSaveError(true);
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  const requestBack = () => {
    if (savingRef.current || artistSaving) return;
    onBack();
  };

  return (
    <OwnCatalogEditorShell
      eyebrow={m("eyebrow")}
      title={initialValues ? m("editRecording") : m("createRecording")}
      backLabel={m("back")}
      backDisabled={saving || artistSaving}
      onBack={requestBack}
    >
      <OwnCatalogRecordEditorView
        notice={m("formRequiredNotice")}
        help={m("durationHelp")}
        fields={[
          {
            label: `${m("titleField")} · ${m("required")}`,
            value: title,
            onChange: setTitle,
            required: true,
            disabled: saving,
          },
          {
            label: `${m("durationField")} · ${m("optional")}`,
            value: duration,
            onChange: (value) => {
              setDuration(value);
              setBadDuration(false);
            },
            placeholder: "3:24",
            disabled: saving,
          },
          {
            label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
            value: tags,
            onChange: setTags,
            disabled: saving,
          },
        ]}
        fieldError={badDuration ? m("badDuration") : undefined}
        genre={
          <GenreCatalogProvider
            model={genreCatalogModel}
            selection={genreCatalogSelection}
          >
            <LocalReleaseGenreCatalogOutlet
              locale={locale}
              owner="recording"
              value={genres}
              onChange={setGenres}
              disabled={saving}
            />
          </GenreCatalogProvider>
        }
        extra={
          <LocalRecordingArtistField
            copy={copy}
            findArtists={findArtists}
            saveArtist={saveArtist}
            credits={credits}
            saved={savedArtists}
            busy={saving}
            onCreditsChange={setCredits}
            onSavingChange={setArtistSaving}
            onSaved={(artist) =>
              setSavedArtists((current) =>
                current.some((item) => item.id === artist.id)
                  ? current
                  : [...current, artist],
              )
            }
          />
        }
        error={
          uncertain
            ? m("saveUncertain")
            : saveError
              ? m("saveError")
              : undefined
        }
        save={{
          label: m("save"),
          disabled: !title.trim() || saving || artistSaving || uncertain,
          onPress: () => void save(),
        }}
        cancel={{
          label: m("cancel"),
          disabled: saving,
          onPress: requestBack,
        }}
      />
    </OwnCatalogEditorShell>
  );
}
