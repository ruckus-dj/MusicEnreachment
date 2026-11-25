import type { LocalProviderItem } from "../../api/generated/client.schemas";
import { OwnCatalogEditorShell } from "../../ui/features/local-catalog/OwnCatalogEditorShell";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "../../ui/features/local-catalog/ownCatalogMessages";
import { LocalReleaseCancelDialog } from "../../ui/features/local-release/LocalReleaseCancelDialog";
import { LocalReleaseForm } from "../../ui/features/local-release/LocalReleaseForm";
import type {
  FindLocalArtists,
  LocalRecordingDraft,
  LocalReleaseArtist,
  LocalReleaseDraft,
  LocalReleaseRecording,
  SaveLocalArtist,
} from "../../ui/features/local-release/localReleaseFormModel";
import { localReleaseMessages } from "../../ui/features/local-release/localReleaseMessages";
import { useLocalReleaseForm } from "../../ui/features/local-release/useLocalReleaseForm";
import type { Locale } from "../../ui/i18n/locale";
import { ProviderCatalogReleaseGenreField } from "../local-release/ProviderCatalogReleaseGenreField";
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
