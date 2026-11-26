import { useState } from "react";
import { useI18n } from "../../../app/i18n";
import type { TrackActionsViewProps } from "../../../ui/features/library/TrackActionsView";
import { operationMessage as message } from "../../../ui/features/operations/operationMessages";
import {
	type OriginalRelease,
	type OriginalTrack,
	originalFiles,
	type SourceFiles,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { refreshProviderHint, renameLocalTrack } from "./trackActionModel";

export type TrackActionsProps = {
	release: OriginalRelease;
	track: OriginalTrack;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	onRepublish?: () => void;
};

export function useTrackActionsDemoProvider({
	release,
	track,
	onMutate,
	onQueueDeletion,
	files = originalFiles,
	onRepublish,
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
		onRepublish:
			track.published && onRepublish
				? () =>
						file ? onRepublish() : setNotice(message(locale, "chooseSource"))
				: undefined,
		deletion:
			track.published && onQueueDeletion
				? { release, track, onQueue: onQueueDeletion }
				: undefined,
	};
}
