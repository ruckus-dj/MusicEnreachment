import type { LocalProviderRecord } from "../../localProviderModel";
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
import { useLocalReleaseForm } from "../local-release/useLocalReleaseForm";
import { LocalCatalogEditorShell } from "./LocalCatalogEditorShell";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import { releaseRecordToFormValues } from "./localCatalogModel";

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
		if (form.hasDraft()) form.setCancel(true);
		else onBack();
	};
	return (
		<>
			<LocalCatalogEditorShell
				eyebrow={m("eyebrow")}
				title={record ? m("editRelease") : m("createRelease")}
				backLabel={m("back")}
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
					genresLoaded={genresLoaded}
					genreOptions={genreOptions}
					onLoadGenres={onLoadGenres}
				/>
			</LocalCatalogEditorShell>
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
