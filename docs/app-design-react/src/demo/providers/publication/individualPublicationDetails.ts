import type { PublicationPreviewDetailsState } from "../../../ui/features/library/PublicationPreviewDetailsView";
import type { ContextPublicationPreviewWorkflow } from "../../../ui/features/publication/ContextPublicationPreview";
import type { OriginalTrack } from "../library/catalogData";

type TagValues = Readonly<Record<string, string[]>>;

export function individualPublicationDetails({
	workflow,
	retainedCapability,
	current,
	tags,
	path,
	renderedPath,
	source,
	coverHash,
	lyrics,
	downgrade,
	admittedOperationID = "",
	error = "",
}: {
	workflow?: ContextPublicationPreviewWorkflow;
	retainedCapability?: "ready" | "unchanged" | "blocked";
	current: OriginalTrack["published"];
	tags: TagValues;
	path?: string;
	renderedPath?: string;
	source?: { codec: string; path: string };
	coverHash: string | null;
	lyrics: string | null;
	downgrade?: boolean;
	admittedOperationID?: string;
	error?: string;
}): PublicationPreviewDetailsState {
	const resolvedWorkflow = workflow ?? (current ? "comparison" : "first");
	const before = current?.tags ?? {};
	const differences = [
		...new Set([...Object.keys(before), ...Object.keys(tags)]),
	]
		.filter((key) => JSON.stringify(before[key]) !== JSON.stringify(tags[key]))
		.map((key) => ({
			key,
			current: before[key],
			current_present: Object.hasOwn(before, key),
			desired: tags[key],
			desired_present: Object.hasOwn(tags, key),
		}));
	const priorCover = current?.coverHash ?? null;
	const priorLyrics = current?.lyrics ?? null;
	return {
		loading: false,
		error,
		admittedOperationID,
		admissionUncertain: false,
		inspection: {
			workflow: resolvedWorkflow,
			sourceCandidates: [],
			selectedChoice: null,
			needsExplicitChoice: false,
			sourceChoiceDisabled: true,
			preview: {
				confirmation_capability: retainedCapability
					? { status: retainedCapability }
					: undefined,
				desired_tags: tags,
				resolved_path: path,
				selected_source: source,
				showAudioComparisonBasis: false,
				warnings: downgrade ? ["output_quality_would_downgrade"] : undefined,
				current_comparison: current
					? {
							current_components: [current.path],
							rendered_components: renderedPath ? [renderedPath] : undefined,
							resolved_components: path ? [path] : undefined,
							audio_status: "",
							cover_status: priorCover === coverHash ? "matches" : "differs",
							tag_differences: differences,
						}
					: undefined,
				asset_changes: {
					cover:
						coverHash !== priorCover
							? { current: priorCover, planned: coverHash }
							: undefined,
					lyrics:
						lyrics !== priorLyrics
							? { current: priorLyrics, planned: lyrics }
							: undefined,
				},
			},
		},
	};
}
