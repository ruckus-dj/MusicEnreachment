import { useState } from "react";
import { useI18n } from "../../../app/i18n";
import type { DeletionOperation } from "../../components/DeletionRequest";
import { operationMessage as message } from "../operations/operationMessages";
import {
	type OriginalRelease,
	type OriginalTrack,
	originalFiles,
	type SourceFiles,
} from "./catalogData";
import type { TrackActionsViewProps } from "./TrackActionsView";
import { refreshProviderHint, renameLocalTrack } from "./trackActionModel";

export type TrackActionsProps = {
	release: OriginalRelease;
	track: OriginalTrack;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	onRepublish: () => void;
	onAudioUpgrade?: () => void;
};

export function useTrackActionsDemoProvider({
	release,
	track,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
	onRepublish,
	onAudioUpgrade,
}: TrackActionsProps): Omit<TrackActionsViewProps, "deletion"> & {
	deletion?: {
		release: OriginalRelease;
		track: OriginalTrack;
		onQueue: (operation: DeletionOperation) => void;
	};
} {
	const { locale } = useI18n();
	const [renaming, setRenaming] = useState(false);
	const [title, setTitle] = useState(track.title);
	const [notice, setNotice] = useState("");
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const open = () => {
		setTitle(track.title);
		setNotice("");
		setRenaming(true);
	};
	return {
		locale,
		notice,
		rename: {
			open: renaming,
			title,
			onOpenChange: setRenaming,
			onTitleChange: setTitle,
			onStart: open,
			onSubmit: () => {
				onMutate((current) => renameLocalTrack(current, title));
				setNotice(message(locale, "renamePreserves"));
				setRenaming(false);
			},
		},
		onRefresh: () => {
			onMutate(refreshProviderHint);
			setNotice(message(locale, "providerCacheRefreshed"));
		},
		onAudioUpgrade:
			track.published?.codec === "MP3" && file?.codec === "FLAC"
				? onAudioUpgrade
				: undefined,
		onRepublish: track.published
			? () =>
					file ? onRepublish() : setNotice(message(locale, "chooseSource"))
			: undefined,
		deletion:
			track.published && onQueueDeletion
				? { release, track, onQueue: onQueueDeletion }
				: undefined,
	};
}
