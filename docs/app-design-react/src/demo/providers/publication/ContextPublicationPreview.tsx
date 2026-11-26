import { type ReactNode, useState } from "react";
import { useI18n } from "../../../app/i18n";
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
	applyPlan,
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
import type { UiNotice } from "../notices/uiNotice";
import { demoPathInside } from "../sources/demoPathValidation";
import { individualPublicationDetails } from "./individualPublicationDetails";
import { publicationArtistNotices } from "./publicationArtistNotices";
import {
	fixturePublicationDestination,
	type PublicationDestination,
	retainedOutputIssue,
} from "./publicationOutput";

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
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: UiNotice) => void;
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
	onReleases,
	onSuccess,
	controls,
	workflow,
	previewStale = false,
	hasCustomTemplate = false,
	children,
}: ContextPublicationPreviewProps) {
	const { locale } = useI18n();
	const positionId = plan.positionId;
	if (!positionId || plan.mode !== "local" || plan.groupId) return null;

	const release = releases.find((item) => item.id === plan.localReleaseId);
	const row = planPositionRow(
		plan,
		files,
		releases,
		providers,
		artists,
		destination,
		positionId,
	);
	const track = row?.track;
	const current = row?.previous;
	const resolvedWorkflow = workflow;
	const retainedWorkflow = resolvedWorkflow === "retained_same_path";
	const readOnly = resolvedWorkflow === "comparison";
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
	const observedCurrent = track?.observedFile;
	const observedCurrentUnavailable = Boolean(
		current &&
			observedCurrent &&
			(observedCurrent.rootAvailable === false ||
				["changed", "drift", "missing", "output-unavailable"].includes(
					observedCurrent.state ?? "",
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
	const details = row
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

	const confirm = () => {
		if (!canConfirm || !row?.assignment || !track) return;
		const previouslyLinked = new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]);
		const hasNewLinks = sourceIds(row.assignment).some(
			(id) => usableFile(files[id]) && !previouslyLinked.has(id),
		);
		const next = structuredClone(releases);
		const applied = applyPlan(
			{ ...plan, assignments: { [positionId]: row.assignment } },
			files,
			next,
			providers,
			false,
			artists,
			destination,
			positionId,
		);
		if (!applied) return;
		onReleases(next);
		onClose();
		onSuccess({
			namespace: "publication",
			id: "applySuccess",
			params: { count: applied, linkCount: hasNewLinks ? 1 : 0 },
		});
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
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: UiNotice) => void;
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
	onReleases,
	onSuccess,
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
			onReleases={onReleases}
			onSuccess={onSuccess}
			workflow={effectiveWorkflow}
			hasCustomTemplate={hasCustomTemplate}
			controls={controls}
		>
			{children}
		</ContextPublicationPreview>
	);
}
