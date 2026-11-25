import { type ComponentType, useRef, useState } from "react";
import type { Locale } from "../../i18n/types";
import type {
  GenrePickerCatalog,
  GenreReference,
} from "../library/GenrePicker";
import {
  LocalReleaseGenreCatalogOutlet,
  type LocalReleaseGenreCatalogProviderProps,
  type LocalReleaseGenreSelectionAdapter,
} from "../local-release/LocalReleaseGenreCatalogOutlet";
import { isOutcomeUncertain } from "../local-release/localReleaseFormModel";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import { OwnCatalogRecordEditorView } from "./OwnCatalogRecordEditorView";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";

/** Portable artist draft; the host adapts the record and wire trimming. */
export type OwnCatalogArtistDraft = {
  name: string;
  sortName: string;
  tags: string;
  genres: GenreReference[];
};

/**
 * Portable Own Catalog artist editor. The host supplies only the initial draft,
 * typed genre catalog lifetime and save/back actions; the portable tree owns the
 * shell, fields, nested genre binder and guarded submit lifecycle.
 */
export function OwnCatalogArtistEditor({
  locale,
  initialValues,
  genreCatalogProvider,
  genreCatalogModel,
  genreCatalogSelection,
  onSave,
  onBack,
}: {
  locale: Locale;
  /** Initial draft projection; absent for a new artist. */
  initialValues?: OwnCatalogArtistDraft;
  /**
   * Stable host provider mounted with this editor opening. Live hosts keep the
   * original catalog hook inside it so a new opening starts with fresh state.
   */
  genreCatalogProvider: ComponentType<LocalReleaseGenreCatalogProviderProps>;
  /** Demo hosts may bind an already-built catalog model instead of a hook. */
  genreCatalogModel?: GenrePickerCatalog;
  /** Demo-only projection for stored draft rows; live leaves this unset. */
  genreCatalogSelection?: LocalReleaseGenreSelectionAdapter;
  onSave: (draft: OwnCatalogArtistDraft) => void | Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const GenreCatalogProvider = genreCatalogProvider;
  const [name, setName] = useState(initialValues?.name ?? "");
  const [sortName, setSortName] = useState(initialValues?.sortName ?? "");
  const [tags, setTags] = useState(initialValues?.tags ?? "");
  const [genres, setGenres] = useState<GenreReference[]>(() => [
    ...(initialValues?.genres ?? []),
  ]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const savingRef = useRef(false);

  async function save() {
    if (savingRef.current || uncertain || !name.trim()) return;
    savingRef.current = true;
    setSaving(true);
    setSaveError(false);
    try {
      await onSave({ name, sortName, tags, genres });
    } catch (error) {
      if (isOutcomeUncertain(error)) setUncertain(true);
      else setSaveError(true);
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  const requestBack = () => {
    if (savingRef.current) return;
    onBack();
  };

  return (
    <OwnCatalogEditorShell
      eyebrow={m("eyebrow")}
      title={initialValues ? m("editArtist") : m("createArtist")}
      backLabel={m("back")}
      backDisabled={saving}
      onBack={requestBack}
    >
      <OwnCatalogRecordEditorView
        notice={m("formRequiredNotice")}
        fields={[
          {
            label: `${m("name")} · ${m("required")}`,
            value: name,
            onChange: setName,
            required: true,
            disabled: saving,
          },
          {
            label: `${m("sortName")} · ${m("optional")}`,
            value: sortName,
            onChange: setSortName,
            disabled: saving,
          },
          {
            label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
            value: tags,
            onChange: setTags,
            disabled: saving,
          },
        ]}
        genre={
          <GenreCatalogProvider
            model={genreCatalogModel}
            selection={genreCatalogSelection}
          >
            <LocalReleaseGenreCatalogOutlet
              locale={locale}
              owner="artist"
              value={genres}
              onChange={setGenres}
              disabled={saving}
            />
          </GenreCatalogProvider>
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
          disabled: !name.trim() || saving || uncertain,
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
