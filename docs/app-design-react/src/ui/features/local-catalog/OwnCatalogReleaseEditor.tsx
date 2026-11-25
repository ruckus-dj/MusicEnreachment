import type { ComponentType } from "react";
import type { Locale } from "../../i18n/types";
import type { GenrePickerCatalog } from "../library/GenrePicker";
import { LocalReleaseCancelDialog } from "../local-release/LocalReleaseCancelDialog";
import { LocalReleaseForm } from "../local-release/LocalReleaseForm";
import {
  LocalReleaseGenreCatalogOutlet,
  type LocalReleaseGenreCatalogProviderProps,
  type LocalReleaseGenreSelectionAdapter,
} from "../local-release/LocalReleaseGenreCatalogOutlet";
import type {
  FindLocalArtists,
  LocalRecordingDraft,
  LocalReleaseArtist,
  LocalReleaseDraft,
  LocalReleaseRecording,
  SaveLocalArtist,
} from "../local-release/localReleaseFormModel";
import { localReleaseMessages } from "../local-release/localReleaseMessages";
import {
  type LocalReleaseFormInitialValues,
  useLocalReleaseForm,
} from "../local-release/useLocalReleaseForm";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";

/**
 * Portable Own Catalog release editor. The host supplies only the initial form
 * projection, roster, typed genre catalog, async lookups and save/back actions;
 * the portable tree owns the shell, form and cancel-dialog composition.
 */
export function OwnCatalogReleaseEditor({
  locale,
  initialValues,
  artists,
  recordings,
  findRecordings,
  findArtists,
  saveRecording,
  saveArtist,
  genreCatalogProvider,
  genreCatalogModel,
  genreCatalogSelection,
  onSave,
  onBack,
}: {
  locale: Locale;
  /** Initial form projection; absent for a new release. */
  initialValues?: LocalReleaseFormInitialValues;
  artists: readonly LocalReleaseArtist[];
  recordings: readonly LocalReleaseRecording[];
  findRecordings: (
    query: string,
    signal?: AbortSignal,
  ) => Promise<readonly LocalReleaseRecording[]>;
  findArtists: FindLocalArtists;
  saveRecording: (
    data: LocalRecordingDraft,
  ) => LocalReleaseRecording | Promise<LocalReleaseRecording>;
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
  onSave: (draft: LocalReleaseDraft) => void | Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const copy = localReleaseMessages[locale];
  const GenreCatalogProvider = genreCatalogProvider;
  const form = useLocalReleaseForm({ initialValues, onSave });
  const requestBack = () => {
    if (
      form.savingRelease ||
      form.writingCatalogRecord ||
      form.releaseUncertain
    )
      return;
    if (form.hasDraft()) form.setCancel(true);
    else onBack();
  };
  return (
    <>
      <OwnCatalogEditorShell
        eyebrow={m("eyebrow")}
        title={initialValues ? m("editRelease") : m("createRelease")}
        backLabel={m("back")}
        backDisabled={form.savingRelease || form.writingCatalogRecord}
        onBack={requestBack}
      >
        <LocalReleaseForm
          locale={locale}
          form={form}
          heading={m("recordDetails")}
          suggestedArtist=""
          artists={artists}
          recordings={recordings}
          findRecordings={findRecordings}
          saveRecording={saveRecording}
          findArtists={findArtists}
          saveArtist={saveArtist}
          renderGenreField={(props) => (
            <GenreCatalogProvider
              model={genreCatalogModel}
              selection={genreCatalogSelection}
            >
              <LocalReleaseGenreCatalogOutlet {...props} />
            </GenreCatalogProvider>
          )}
          saveLabel={m("save")}
        />
      </OwnCatalogEditorShell>
      <LocalReleaseCancelDialog
        copy={copy}
        isOpen={form.cancel}
        onOpenChange={form.setCancel}
        onDiscard={onBack}
        savedRecordings={form.savedRecordings}
        savedArtists={form.savedArtists}
      />
    </>
  );
}
