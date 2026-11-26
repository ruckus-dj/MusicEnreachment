import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import { LibraryReleaseWorkspaceView } from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import type {
	TrackProviderRefreshModel,
	TrackScreenNotFoundView,
} from "../../../ui/features/library/TrackScreenView";
import type {
	DemoPositionPublicationBinderProps,
	DemoPositionPublicationSource,
} from "../publication/ContextPublicationPreview";
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
	contextualPublications?: ReadonlyMap<string, DemoPositionPublicationSource>;
	providerRefresh?: TrackProviderRefreshModel;
};

/**
 * One release workspace keyed by release id. The overview starts unmounted on a
 * cold direct position and mounts on the first edition visit; afterwards it
 * stays retained while that release's positions are selected. The retained
 * overview keeps its metadata draft provider alive but hides its DOM and
 * portals behind a position. Viewed positions retain their exact publication
 * binder; other position panels only render for the active route.
 */
function DemoLibraryReleaseWorkspaceForRelease({
	overview,
	position = null,
	notFound,
	contextualPublications,
	providerRefresh,
}: DemoLibraryReleaseWorkspaceProps) {
	const { locale } = useI18n();
	const showPosition = position !== null;
	const [visitedEdition, setVisitedEdition] = useState(!showPosition);
	if (!showPosition && !visitedEdition) setVisitedEdition(true);
	const positionKey = position ? position.track.id : null;
	const [visitedPositions, setVisitedPositions] = useState<readonly string[]>(
		[],
	);
	const positionKeys =
		positionKey && !visitedPositions.includes(positionKey)
			? [...visitedPositions, positionKey]
			: visitedPositions;
	if (positionKeys !== visitedPositions) setVisitedPositions(positionKeys);
	const overviewModel = useReleaseOverviewDemoModel({
		...overview,
		editionControls: !showPosition && notFound === undefined,
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
			overviewProviderRefresh={
				!showPosition && !notFound ? providerRefresh : undefined
			}
			positions={positionKeys.map((id) => ({
				id,
				screen: {
					...(id === positionKey ? { ...positionModel, providerRefresh } : {}),
					contextualPublication:
						contextualPublications?.get(id) ??
						(id === positionKey
							? positionModel?.contextualPublication
							: undefined),
				},
			}))}
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
