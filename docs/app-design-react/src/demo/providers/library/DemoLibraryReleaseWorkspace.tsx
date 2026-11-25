import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import { LibraryReleaseWorkspaceView } from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import type { TrackScreenNotFoundView } from "../../../ui/features/library/TrackScreenView";
import type { DemoPositionPublicationBinderProps } from "../publication/ContextPublicationPreview";
import {
	type ReleaseOverviewScreenProps,
	useReleaseOverviewDemoModel,
} from "./useReleaseOverviewDemoModel";
import {
	type TrackScreenProps,
	useTrackScreenDemoProvider,
} from "./useTrackScreenDemoProvider";

export type DemoLibraryReleaseWorkspaceProps = {
	/** Release facts, selection and actors for the retained overview. */
	overview: ReleaseOverviewScreenProps;
	/** Active position actors; `null` (or omitted) selects the edition. */
	position?: TrackScreenProps | null;
	/** Requested position absent from the selected release; keeps its overview. */
	notFound?: TrackScreenNotFoundView;
};

/**
 * One release workspace keyed by release id. The overview starts unmounted on a
 * cold direct position and mounts on the first edition visit; afterwards it
 * stays retained while that release's positions are selected. The retained
 * overview keeps its metadata draft provider alive but hides its DOM and
 * portals behind a position, while the single active position gets its own
 * session that resets on every position entry or return to the edition.
 */
function DemoLibraryReleaseWorkspaceForRelease({
	overview,
	position = null,
	notFound,
}: DemoLibraryReleaseWorkspaceProps) {
	const { locale } = useI18n();
	const showPosition = position !== null;
	const [visitedEdition, setVisitedEdition] = useState(!showPosition);
	if (!showPosition && !visitedEdition) setVisitedEdition(true);
	const positionKey = position ? position.track.id : null;
	const overviewModel = useReleaseOverviewDemoModel({
		...overview,
		editionControls: !showPosition && notFound === undefined,
		visible: !showPosition,
	});
	const positionModel = useTrackScreenDemoProvider(position);
	return (
		<LibraryReleaseWorkspaceView<DemoPositionPublicationBinderProps>
			locale={locale}
			selection={
				notFound
					? { kind: "missing", notFound }
					: positionKey
						? { kind: "position", positionId: positionKey }
						: { kind: "edition" }
			}
			overview={visitedEdition ? overviewModel : null}
			positions={
				positionKey && positionModel
					? [{ id: positionKey, screen: positionModel }]
					: []
			}
		/>
	);
}

/**
 * Module-level release workspace for the prototype host. The key prevents
 * retention state and the metadata actor from following navigation into a
 * different release; same-release edition/position changes keep one instance.
 */
export function DemoLibraryReleaseWorkspace(
	props: DemoLibraryReleaseWorkspaceProps,
) {
	return (
		<DemoLibraryReleaseWorkspaceForRelease
			key={props.overview.release.id}
			{...props}
		/>
	);
}
