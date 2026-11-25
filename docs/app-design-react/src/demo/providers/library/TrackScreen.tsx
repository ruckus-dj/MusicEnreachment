import { useI18n } from "../../../app/i18n/host";
import { LibraryReleaseWorkspaceView } from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import {
	type TrackScreenProps,
	useTrackScreenDemoProvider,
} from "./useTrackScreenDemoProvider";

/**
 * Public track screen entry point. The demo provider owns selection state,
 * derived publication status and nested domain actors; the shared workspace
 * mounts the position model. This direct demo route has no retained release
 * overview, so the workspace overview stays explicitly unmounted.
 */
export function TrackScreen(props: TrackScreenProps) {
	const { locale } = useI18n();
	const screen = useTrackScreenDemoProvider(props);
	return (
		<LibraryReleaseWorkspaceView
			locale={locale}
			selection={{ kind: "position", positionId: props.track.id }}
			overview={null}
			positions={[{ id: props.track.id, screen }]}
		/>
	);
}
