import { useI18n } from "../../i18n";
import { demoPathInside } from "../../infrastructure/demoPathValidation";
import type { UiNotice } from "../../infrastructure/uiNotice";
import {
	applyPlan,
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
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
import { ContextPublicationPreviewView } from "./ContextPublicationPreviewView";
import { individualPublicationDetails } from "./individualPublicationDetails";
import { publicationArtistNotices } from "./publicationArtistNotices";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";
import {
	choosePublicationOutput,
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";

function capturePosition(release: OriginalRelease, positionId: string) {
	const { tracks, ...releaseFields } = release;
	return JSON.stringify([
		releaseFields,
		tracks.find((track) => track.id === positionId),
	]);
}

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
}: {
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
}) {
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
	const readOnly = Boolean(current && !plan.force);
	const positionChanged =
		!release ||
		track?.id !== positionId ||
		capturePosition(plan.target, positionId) !==
			capturePosition(release, positionId);
	const inventoryChanged = plan.inventoryVersion !== inventoryVersion;
	const currentOutsideOutput = Boolean(
		plan.force &&
			current &&
			!demoPathInside(destination.outputDirectory, current.path),
	);
	const sourceId = selectedSourceId(row?.assignment);
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
				: "";
	const source = row?.file;
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
			path: rebuildSource ? output?.path : (current?.path ?? output?.path),
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

	return (
		<ContextPublicationPreviewView
			locale={locale}
			error={error}
			readOnly={readOnly}
			reason={reason}
			notices={notices}
			details={details}
			sourceGroupName={`demo-publication-source-${positionId}`}
			downgradeControl={
				downgrade
					? {
							checked: confirmDowngrade,
							disabled: Boolean(error),
							label: formatPublicationMessage(locale, "confirmDowngrade", {
								count: 1,
							}),
							onChange: (checked) => onPlan(plan, checked),
						}
					: undefined
			}
			confirmControl={{
				label: current ? "retainedRepublish" : "publish",
				disabled: !canConfirm,
				onConfirm: confirm,
			}}
		/>
	);
}
