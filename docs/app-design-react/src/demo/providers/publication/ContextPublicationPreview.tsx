import { type ReactNode, useState } from "react";
import { useI18n } from "../../../app/i18n";
import type { PublicationPreviewSource } from "../../../ui/features/library/PublicationPreviewDetailsView";
import {
	ContextPublicationPreviewDataBinder,
	type ContextPublicationPreviewMode,
	type ContextPublicationPreviewModel,
	type ContextPublicationPreviewSource,
	type ContextPublicationPreviewWorkflow,
} from "../../../ui/features/publication/ContextPublicationPreview";
import {
	formatPublicationMessage,
	publicationMessage,
} from "../../../ui/features/publication/publicationMessages";
import {
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
	type IncomingState,
	makeReleasePlan,
	planPositionRow,
	selectedSourceId,
	sourceIds,
	type TagMap,
	usableFile,
} from "../incoming/incomingModel";
import {
	type CatalogArtist,
	type OriginalRelease,
	publicationMetadataIssueMessages,
} from "../library/catalogData";
import { sameTagValues } from "../library/tagEquality";
import {
	contributorProjectionIntent,
	omitsStructuredPerformer,
} from "../library/metadata/scopedContributors";
import { demoPathInside } from "../sources/demoPathValidation";
import {
	type ContextualPublicationPreparation,
	prepareContextualPublication,
	retainedCurrentUnavailable,
} from "./contextualPublicationJob";
import { individualPublicationDetails } from "./individualPublicationDetails";
import { publicationPathOwned } from "./libraryPublicationDemo";
import { publicationArtistNotices } from "./publicationArtistNotices";
import {
	fixturePublicationDestination,
	type PublicationDestination,
	retainedOutputIssue,
} from "./publicationOutput";

/**
 * A demo source file is one analyzed media variant with one audio stream. Its
 * known location and variant are the only source identities available here, so
 * stream `0` is the variant's single stream rather than a fabricated choice.
 */
function publicationSourceCandidates(
	plan: IncomingPlan,
	files: IncomingFiles,
	positionId: string,
): PublicationPreviewSource[] {
	const assignment = plan.assignments[positionId];
	if (!assignment) return [];
	const selectedId = selectedSourceId(assignment);
	const linked = [
		...new Set([...sourceIds(assignment), ...(selectedId ? [selectedId] : [])]),
	];
	return linked.flatMap((id) => {
		const file = files[id];
		const location = file?.locations?.[0];
		if (!file || !location || !file.codec || !usableFile(file)) return [];
		return [
			{
				audio_stream_count: 1,
				codec: file.codec,
				language: "",
				location_id: location.id,
				media_variant_id: file.id,
				requires_explicit_stream_selection: false,
				root_id: location.rootId,
				stream_index: 0,
				title: file.name,
				unsupported_audio_stream_count: 0,
			},
		];
	});
}

function planWithSource(
	plan: IncomingPlan,
	positionId: string,
	sourceId: string | null,
) {
	const assignment = plan.assignments[positionId];
	if (!assignment || selectedSourceId(assignment) === sourceId) return plan;
	return {
		...plan,
		assignments: {
			...plan.assignments,
			[positionId]: { ...assignment, selectedSourceId: sourceId },
		},
	};
}

function capturePosition(release: OriginalRelease, positionId: string) {
	const { tracks, ...releaseFields } = release;
	return JSON.stringify([
		releaseFields,
		tracks.find((track) => track.id === positionId),
	]);
}

type PositionPreviewSnapshot = {
	destination: PublicationDestination;
	source: { id: string; codec: string; path: string } | null;
	renderedPath?: string;
	renderedContainer?: string;
	tags: TagMap;
};

function capturePositionPreview(
	plan: IncomingPlan,
	destination: PublicationDestination,
	files: IncomingFiles,
	releases: IncomingReleases,
	providers: IncomingReleases,
	artists: CatalogArtist[],
	positionId: string,
): PositionPreviewSnapshot | null {
	const row = planPositionRow(
		plan,
		files,
		releases,
		providers,
		artists,
		destination,
		positionId,
	);
	if (!row) return null;
	return {
		destination: { ...destination },
		source: row.file
			? { id: row.file.id, codec: row.file.codec, path: row.file.path }
			: null,
		renderedPath: row.renderedPath,
		renderedContainer: row.renderedContainer,
		tags: structuredClone(row.nextTags),
	};
}

function samePositionPreview(
	left: PositionPreviewSnapshot | null,
	right: PositionPreviewSnapshot | null,
) {
	if (!left || !right) return left === right;
	const sameSource =
		left.source === null || right.source === null
			? left.source === right.source
			: left.source.id === right.source.id &&
				left.source.codec === right.source.codec &&
				left.source.path === right.source.path;
	return (
		left.destination.outputDirectory === right.destination.outputDirectory &&
		left.destination.format === right.destination.format &&
		sameSource &&
		left.renderedPath === right.renderedPath &&
		left.renderedContainer === right.renderedContainer &&
		sameTagValues(left.tags, right.tags)
	);
}

export type ContextPublicationPreviewProps = {
	destination?: PublicationDestination;
	plan: IncomingPlan;
	files: IncomingFiles;
	releases: IncomingReleases;
	providers: IncomingReleases;
	artists: CatalogArtist[];
	inventoryVersion: number;
	onClose: () => void;
	onQueue: (prepared: ContextualPublicationPreparation) => void;
	controls?: Pick<
		ContextPublicationPreviewModel,
		"workflowControl" | "planControl"
	>;
	workflow: ContextPublicationPreviewWorkflow;
	previewStale?: boolean;
	hasCustomTemplate?: boolean;
	children?: ReactNode;
};

export type ContextPublicationPreviewBinderProps = Omit<
	ContextPublicationPreviewProps,
	"children"
>;

export function ContextPublicationPreview({
	destination = fixturePublicationDestination,
	plan,
	files,
	releases,
	providers,
	artists,
	inventoryVersion,
	onClose,
	onQueue,
	controls,
	workflow,
	previewStale = false,
	hasCustomTemplate = false,
	children,
}: ContextPublicationPreviewProps) {
	const { locale } = useI18n();
	const [chosenSourceId, setChosenSourceId] = useState<string | null>();
	const positionId = plan.positionId;
	if (!positionId || plan.mode !== "local" || plan.groupId) return null;

	const resolvedWorkflow = workflow;
	const retainedWorkflow = resolvedWorkflow === "retained_same_path";
	const readOnly = resolvedWorkflow === "comparison";
	const assignment = plan.assignments[positionId];
	const defaultSourceId = selectedSourceId(assignment);
	const sourceCandidates =
		retainedWorkflow || plan.force
			? []
			: publicationSourceCandidates(plan, files, positionId);
	const chosenCandidate =
		chosenSourceId === undefined || chosenSourceId === null
			? undefined
			: sourceCandidates.find(
					({ media_variant_id }) => media_variant_id === chosenSourceId,
				);
	const selectedChoice = chosenCandidate
		? {
				location_id: chosenCandidate.location_id,
				media_variant_id: chosenCandidate.media_variant_id,
				stream_index: chosenCandidate.stream_index,
			}
		: null;
	const effectiveSourceId =
		chosenSourceId === undefined
			? defaultSourceId
			: (chosenCandidate?.media_variant_id ?? null);
	const effectivePlan =
		effectiveSourceId === defaultSourceId
			? plan
			: planWithSource(plan, positionId, effectiveSourceId);
	const needsExplicitChoice = sourceCandidates.length > 1;
	const release = releases.find((item) => item.id === plan.localReleaseId);
	const row = planPositionRow(
		effectivePlan,
		files,
		releases,
		providers,
		artists,
		destination,
		positionId,
	);
	const track = row?.track;
	const current = row?.previous;
	const positionChanged =
		!release ||
		track?.id !== positionId ||
		capturePosition(plan.target, positionId) !==
			capturePosition(release, positionId);
	const inventoryChanged = plan.inventoryVersion !== inventoryVersion;
	const currentOutsideOutput = Boolean(
		!readOnly &&
			plan.force &&
			current &&
			!demoPathInside(destination.outputDirectory, current.path),
	);
	const sourceId = selectedSourceId(row?.assignment);
	const source = row?.file;
	const metadataIssues =
		release && track
			? publicationMetadataIssueMessages(release, track, locale, sourceId)
			: [];
	const observedCurrentUnavailable = Boolean(
		current && retainedCurrentUnavailable(track?.observedFile),
	);
	const outputPath = row?.retainedPath ?? row?.renderedPath;
	const outputBlocked = Boolean(
		!readOnly &&
			release &&
			outputPath &&
			(!demoPathInside(destination.outputDirectory, outputPath) ||
				publicationPathOwned(
					{ releases },
					{ release_id: release.id, position_id: positionId },
					outputPath,
				)),
	);
	const templateUnsupported = Boolean(hasCustomTemplate);
	const retainedIssue =
		retainedWorkflow && current && source
			? retainedOutputIssue(current, source, destination)
			: undefined;
	const retainedSourceMismatch = Boolean(
		retainedWorkflow && current && source && current.sourceId !== source.id,
	);
	const retainedUnavailable = Boolean(
		retainedWorkflow &&
			(!current ||
				!source ||
				retainedSourceMismatch ||
				retainedIssue ||
				observedCurrentUnavailable ||
				outputBlocked ||
				metadataIssues.length > 0 ||
				templateUnsupported),
	);
	const retainedStatus: "ready" | "unchanged" | "blocked" | undefined =
		retainedWorkflow
			? retainedUnavailable
				? "blocked"
				: row?.ordinaryUnchanged && !plan.force
					? "unchanged"
					: "ready"
			: undefined;
	const error = previewStale
		? publicationMessage(locale, "stalePreview")
		: positionChanged
			? publicationMessage(locale, "stalePreview")
			: inventoryChanged
				? publicationMessage(locale, "inventoryChanged")
				: templateUnsupported
					? publicationMessage(locale, "pathUndetermined")
					: currentOutsideOutput
						? publicationMessage(locale, "currentOutsideOutput")
						: retainedIssue
							? publicationMessage(locale, retainedIssue)
							: outputBlocked
								? publicationMessage(locale, "pathUndetermined")
								: "";
	const rebuildSource = Boolean(
		source &&
			(!current ||
				plan.force ||
				current.sourceId !== source.id ||
				current.codec !== row?.outputCodec),
	);
	const downgrade = Boolean(row?.downgrade && row.assignment?.included);
	const canConfirm = Boolean(
		!error &&
			!templateUnsupported &&
			!readOnly &&
			(!retainedWorkflow || retainedStatus === "ready") &&
			(!plan.force || current) &&
			(!needsExplicitChoice || selectedChoice !== null) &&
			row?.assignment?.included &&
			source &&
			metadataIssues.length === 0 &&
			(row.action === "Создать" || row.action === "Заменить"),
	);
	const reason = metadataIssues.length
		? formatPublicationMessage(locale, "invalidMetadata", {
				fields: metadataIssues.join("; "),
			})
		: publicationMessage(
				locale,
				!source
					? sourceId
						? "selectedFileUnavailable"
						: "noSource"
					: row?.action === "Без изменений"
						? "sourceAndTagsSame"
						: current
							? "selectedMetadataAndAudio"
							: "newPublicationSelectedSource",
			);
	const notices: string[] =
		release && track
			? publicationArtistNotices(release, track, artists, locale)
			: [];
	const outputContainer = (
		rebuildSource
			? row?.renderedContainer
			: (current?.container ?? row?.renderedContainer)
	)?.toUpperCase();
	if (
		outputContainer &&
		omitsStructuredPerformer(
			contributorProjectionIntent(
				track?.positionContributors,
				track?.recordingContributors,
				"publication",
			),
			outputContainer,
		)
	) {
		notices.push(
			locale === "ru"
				? `Данные исполнителей не попадут в ${outputContainer}: запись через TagLib не сохраняет их структуру и инструменты.`
				: `structured performer data is omitted from ${outputContainer} output because the TagLib writer cannot preserve its performer and instrument/subrole structure`,
		);
	}
	const detailsState = row
		? individualPublicationDetails({
				workflow: resolvedWorkflow,
				retainedCapability: retainedStatus,
				current: current ?? null,
				tags: row.nextTags,
				path:
					row.retainedPath ??
					(rebuildSource
						? row.renderedPath
						: (current?.path ?? row.renderedPath)),
				renderedPath: row.renderedPath,
				source: source ? { codec: source.codec, path: source.path } : undefined,
				coverHash: track?.coverHash ?? null,
				lyrics: track?.lyrics ?? null,
				downgrade,
			})
		: undefined;
	const details = detailsState
		? {
				...detailsState,
				inspection: {
					...detailsState.inspection,
					sourceCandidates,
					selectedChoice,
					needsExplicitChoice,
					sourceChoiceDisabled: false,
				},
			}
		: undefined;

	const confirm = () => {
		if (!canConfirm || !row?.assignment || !track) return;
		const prepared = prepareContextualPublication(
			{
				releases,
				files,
				providers,
				artists,
				destination,
				inventoryVersion,
				hasCustomTemplate,
			},
			effectivePlan,
		);
		if (!prepared) return;
		onQueue(prepared);
		onClose();
	};

	const model: ContextPublicationPreviewModel = {
		...controls,
		locale,
		error,
		readOnly,
		reason,
		notices,
		details,
		sourceGroupName: `demo-publication-source-${positionId}`,
		onSourceChoice: needsExplicitChoice
			? (choice) => {
					setChosenSourceId(choice ? choice.media_variant_id : null);
				}
			: undefined,
		confirmControl: {
			label: current ? "retainedRepublish" : "publish",
			disabled: !canConfirm,
			onConfirm: confirm,
		},
	};

	return (
		<ContextPublicationPreviewDataBinder model={model}>
			{children}
		</ContextPublicationPreviewDataBinder>
	);
}

export type DemoPositionPublicationProps = {
	destination: PublicationDestination;
	release: OriginalRelease;
	positionId: string;
	state: IncomingState;
	files: IncomingFiles;
	releases: IncomingReleases;
	providers: IncomingReleases;
	artists: CatalogArtist[];
	onQueue: (prepared: ContextualPublicationPreparation) => void;
	hasCustomTemplate?: boolean;
	children?: ReactNode;
};

export type DemoPositionPublicationBinderProps = Omit<
	DemoPositionPublicationProps,
	"children"
>;

export type DemoPositionPublicationSource =
	ContextPublicationPreviewSource<DemoPositionPublicationBinderProps>;

type PreparedPublication = {
	plan: IncomingPlan;
	destination: PublicationDestination;
	preview: PositionPreviewSnapshot | null;
};

export function DemoPositionPublication({
	destination,
	release,
	positionId,
	state,
	files,
	releases,
	providers,
	artists,
	onQueue,
	hasCustomTemplate = false,
	children,
}: DemoPositionPublicationProps) {
	const { locale } = useI18n();
	const [prepared, setPrepared] = useState<PreparedPublication>();
	const [workflow, setWorkflow] = useState<"comparison" | "retained_same_path">(
		"comparison",
	);
	const [mode, setMode] = useState<ContextPublicationPreviewMode>("ordinary");
	const hasCurrent = Boolean(
		release.tracks.find(({ id }) => id === positionId)?.published,
	);
	const effectiveWorkflow: ContextPublicationPreviewWorkflow = hasCurrent
		? workflow
		: "first";
	const compare = effectiveWorkflow === "comparison";
	const confirmable = effectiveWorkflow !== "comparison";
	const planControl = {
		disabled: false,
		onPreview: () => {
			const plan = makeReleasePlan(state, release, files, positionId);
			const preparedPlan = {
				...plan,
				force: hasCurrent && mode === "forced",
				retainCurrentPath: hasCurrent && workflow === "retained_same_path",
			};
			const destinationSnapshot = { ...destination };
			setPrepared({
				plan: preparedPlan,
				destination: destinationSnapshot,
				preview: capturePositionPreview(
					preparedPlan,
					destinationSnapshot,
					files,
					releases,
					providers,
					artists,
					positionId,
				),
			});
		},
	};
	const controls = {
		planControl,
		workflowControl: hasCurrent
			? {
					disabled: false,
					namePrefix: `${release.id}-${positionId}`,
					workflow,
					mode,
					onChange: (next: typeof workflow) => {
						setWorkflow(next);
						setPrepared(undefined);
					},
					onModeChange: (next: ContextPublicationPreviewMode) => {
						setMode(next);
						setPrepared(undefined);
					},
				}
			: undefined,
	};
	const currentPreview = prepared
		? capturePositionPreview(
				prepared.plan,
				destination,
				files,
				releases,
				providers,
				artists,
				positionId,
			)
		: null;
	const previewStale = Boolean(
		prepared && !samePositionPreview(prepared.preview, currentPreview),
	);
	if (!prepared) {
		const model: ContextPublicationPreviewModel = {
			...controls,
			locale,
			sourceGroupName: `demo-publication-source-${positionId}`,
			readOnly: compare,
			confirmControl: confirmable
				? {
						label: hasCurrent ? "retainedRepublish" : "publish",
						disabled: true,
						onConfirm: () => {},
					}
				: undefined,
		};
		return (
			<ContextPublicationPreviewDataBinder model={model}>
				{children}
			</ContextPublicationPreviewDataBinder>
		);
	}
	return (
		<ContextPublicationPreview
			destination={prepared.destination}
			previewStale={previewStale}
			plan={prepared.plan}
			files={files}
			releases={releases}
			providers={providers}
			artists={artists}
			inventoryVersion={state.inventoryVersion}
			onClose={() => setPrepared(undefined)}
			onQueue={onQueue}
			workflow={effectiveWorkflow}
			hasCustomTemplate={hasCustomTemplate}
			controls={controls}
		>
			{children}
		</ContextPublicationPreview>
	);
}
