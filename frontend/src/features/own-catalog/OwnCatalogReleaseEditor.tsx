import type { LocalProviderItem } from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";
import { LocalReleaseCancelDialog } from "../local-release/LocalReleaseCancelDialog";
import { LocalReleaseForm } from "../local-release/LocalReleaseForm";
import type {
  FindLocalArtists,
  LocalRecordingDraft,
  LocalReleaseArtist,
  LocalReleaseDraft,
  LocalReleaseRecording,
  SaveLocalArtist,
} from "../local-release/localReleaseFormModel";
import { localReleaseMessages } from "../local-release/localReleaseMessages";
import { ProviderCatalogReleaseGenreField } from "../local-release/ProviderCatalogReleaseGenreField";
import { useLocalReleaseForm } from "../local-release/useLocalReleaseForm";
import { OwnCatalogEditorShell } from "./OwnCatalogEditorShell";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";
import { releaseRecordToFormValues } from "./ownCatalogModel";

export function OwnCatalogReleaseEditor({
  locale,
  record,
  artists,
  recordings,
  findRecordings,
  findArtists,
  saveRecording,
  saveArtist,
  onSave,
  onBack,
}: {
  locale: Locale;
  record?: LocalProviderItem;
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
  onSave: (draft: LocalReleaseDraft) => void | Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const copy = localReleaseMessages[locale];
  const form = useLocalReleaseForm({
    initialValues: record ? releaseRecordToFormValues(record) : undefined,
    onSave,
  });
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
        title={record ? m("editRelease") : m("createRelease")}
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
            <ProviderCatalogReleaseGenreField {...props} />
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
