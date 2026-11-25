import { OwnCatalogEditorShell } from "../../../ui/features/local-catalog/OwnCatalogEditorShell";
import { LocalReleaseCancelDialog } from "../../../ui/features/local-release/LocalReleaseCancelDialog";
import { LocalReleaseForm } from "../../../ui/features/local-release/LocalReleaseForm";
import type {
	FindLocalArtists,
	LocalRecordingDraft,
	LocalReleaseArtist,
	LocalReleaseDraft,
	LocalReleaseRecording,
	SaveLocalArtist,
} from "../../../ui/features/local-release/localReleaseFormModel";
import { localReleaseMessages } from "../../../ui/features/local-release/localReleaseMessages";
import { useLocalReleaseForm } from "../../../ui/features/local-release/useLocalReleaseForm";
import { LocalReleaseDemoGenres } from "../../fixtures/LocalReleaseDemoGenres";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import { releaseRecordToFormValues } from "./localCatalogModel";
import type { LocalProviderRecord } from "./localProviderModel";

export function LocalCatalogReleaseEditor({
	locale,
	record,
	artists,
	recordings,
	findRecordings,
	findArtists,
	saveRecording,
	saveArtist,
	genresLoaded,
	genreOptions,
	onLoadGenres,
	onSave,
	onBack,
}: {
	locale: "ru" | "en";
	record?: LocalProviderRecord;
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
	genresLoaded?: boolean;
	genreOptions?: readonly { id: string; name: string }[];
	onLoadGenres?: () => void | Promise<void>;
	onSave: (draft: LocalReleaseDraft) => void | Promise<void>;
	onBack: () => void;
}) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	const copy = localReleaseMessages[locale];
	const form = useLocalReleaseForm({
		initialValues: record ? releaseRecordToFormValues(record) : undefined,
		onSave,
	});
	const requestBack = () => {
		if (form.savingRelease || form.writingCatalogRecord) return;
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
					saveLabel={m("save")}
					renderGenreField={(props) => (
						<LocalReleaseDemoGenres
							{...props}
							loaded={genresLoaded ?? false}
							options={genreOptions}
							onLoad={onLoadGenres}
						/>
					)}
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
