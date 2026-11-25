import { type ReactNode, useState } from "react";
import { useI18n } from "../../../app/i18n";
import {
	ContextPublicationPreviewDataBinder,
	type ContextPublicationPreviewMode,
	type ContextPublicationPreviewModel,
	type ContextPublicationPreviewSource,
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
	planRows,
	selectedSourceId,
	sourceIds,
	usableFile,
} from "../incoming/incomingModel";
import {
	type CatalogArtist,
	type OriginalRelease,
	originalArtistName,
	publicationMetadataIssueMessages,
} from "../library/catalogData";
import type { UiNotice } from "../notices/uiNotice";
import { demoPathInside } from "../sources/demoPathValidation";
import { individualPublicationDetails } from "./individualPublicationDetails";
import { publicationArtistNotices } from "./publicationArtistNotices";
import {
	choosePublicationOutput,
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

export type ContextPublicationPreviewProps = {
	destination?: PublicationDestination;
	plan: IncomingPlan;
	files: IncomingFiles;
	releases: IncomingReleases;
	providers: IncomingReleases;
	artists: CatalogArtist[];
	inventoryVersion: number;
	confirmDowngrade: boolean;
	onPlan: (plan: IncomingPlan | null, confirmDowngrade?: boolean) => void;
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: UiNotice) => void;
	controls?: Pick<
		ContextPublicationPreviewModel,
		"workflowControl" | "planControl"
	>;
	comparisonOnly?: boolean;
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
	confirmDowngrade,
	onPlan,
	onReleases,
	onSuccess,
	controls,
	comparisonOnly = false,
	children,
}: ContextPublicationPreviewProps) {
	const { locale } = useI18n();
	const positionId = plan.positionId;
	if (!positionId || plan.mode !== "local" || plan.groupId) return null;

	const release = releases.find((item) => item.id === plan.localReleaseId);
	const row = planRows(
		plan,
		files,
		releases,
		providers,
		artists,
		destination,
	).find((item) => item.targetTrack.id === positionId);
	const track = row?.track;
	const current = row?.previous;
	const readOnly = comparisonOnly || Boolean(current && !plan.force);
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
	const error = positionChanged
		? publicationMessage(locale, "stalePreview")
		: inventoryChanged
			? publicationMessage(locale, "inventoryChanged")
			: currentOutsideOutput
				? publicationMessage(locale, "currentOutsideOutput")
				: plan.retainCurrentPath && current && source
					? (() => {
							const issue = retainedOutputIssue(current, source, destination);
							return issue ? publicationMessage(locale, issue) : "";
						})()
					: "";
	const output =
		source && row
			? choosePublicationOutput(source, {
					...destination,
					artist: originalArtistName(plan.target.artistId, artists),
					year: plan.target.year,
					release: plan.target.title,
					position: row.targetTrack.position,
					title: row.targetTrack.title,
				})
			: undefined;
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
			!readOnly &&
			(!plan.force || current) &&
			row?.assignment?.included &&
			source &&
			metadataIssues.length === 0 &&
			(row.action === "Создать" || row.action === "Заменить") &&
			(!downgrade || confirmDowngrade),
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
	const notices =
		release && track
			? publicationArtistNotices(release, track, artists, locale)
			: [];
	const details =
		row &&
		individualPublicationDetails({
			current: current ?? null,
			tags: row.nextTags,
			path:
				plan.retainCurrentPath && current
					? current.path
					: rebuildSource
						? output?.path
						: (current?.path ?? output?.path),
			renderedPath: output?.path,
			source: source ? { codec: source.codec, path: source.path } : undefined,
			coverHash: track?.coverHash ?? null,
			lyrics: track?.lyrics ?? null,
			downgrade,
		});

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
		);
		if (!applied) return;
		onReleases(next);
		onPlan(null, false);
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
		downgradeControl: downgrade
			? {
					checked: confirmDowngrade,
					disabled: Boolean(error),
					label: formatPublicationMessage(locale, "confirmDowngrade", {
						count: 1,
					}),
					onChange: (checked) => onPlan(plan, checked),
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
	onReleases: (releases: OriginalRelease[]) => void;
	onSuccess: (notice: UiNotice) => void;
	children?: ReactNode;
};

export type DemoPositionPublicationBinderProps = Omit<
	DemoPositionPublicationProps,
	"children"
>;

export type DemoPositionPublicationSource =
	ContextPublicationPreviewSource<DemoPositionPublicationBinderProps>;

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
	children,
}: DemoPositionPublicationProps) {
	const { locale } = useI18n();
	const [prepared, setPrepared] = useState<{
		plan: IncomingPlan;
		confirmDowngrade: boolean;
	}>();
	const [workflow, setWorkflow] = useState<"comparison" | "retained_same_path">(
		"comparison",
	);
	const [mode, setMode] = useState<ContextPublicationPreviewMode>("ordinary");
	const hasCurrent = Boolean(
		release.tracks.find(({ id }) => id === positionId)?.published,
	);
	const compare = hasCurrent && workflow === "comparison";
	const confirmable = !hasCurrent || !compare;
	const planControl = {
		disabled: false,
		onPreview: () => {
			const plan = makeReleasePlan(state, release, files, positionId);
			setPrepared({
				plan: {
					...plan,
					force: hasCurrent && (!compare || mode === "forced"),
					retainCurrentPath: hasCurrent && !compare,
				},
				confirmDowngrade: false,
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
			destination={destination}
			plan={prepared.plan}
			files={files}
			releases={releases}
			providers={providers}
			artists={artists}
			inventoryVersion={state.inventoryVersion}
			confirmDowngrade={prepared.confirmDowngrade}
			onPlan={(plan, confirmed) =>
				setPrepared(
					plan ? { plan, confirmDowngrade: Boolean(confirmed) } : undefined,
				)
			}
			onReleases={onReleases}
			onSuccess={onSuccess}
			comparisonOnly={compare}
			controls={controls}
		>
			{children}
		</ContextPublicationPreview>
	);
}
