import type { ReactNode, Ref } from "react";
import { surfacePanel } from "../../components/styles/matchingStyles";
import { Button, Input, TextField } from "../../components/ui";
import { MatchingBoard, type MatchingBoardProps } from "./MatchingBoard";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";
import { MatchingLinkResultView } from "./MatchingLinkResultView";
import { MatchingPreview, type MatchingPreviewRow } from "./MatchingPreview";
import { MatchingReviewSearchView } from "./MatchingReviewSearchView";
import {
	MatchingWorkspaceLayout,
	matchingInspectorContent,
	matchingInspectorPanel,
} from "./MatchingWorkspaceLayout";
import type {
	MatchingWorkspaceLocale,
	MatchingWorkspacePosition,
} from "./types";

export type MatchingScreenViewLink = {
	key: string;
	source: string;
	positionId: string;
	position: string;
};

export type MatchingScreenViewRemainingFile = {
	key: string;
	name: string;
};

export type MatchingScreenViewBoard = Omit<
	MatchingBoardProps,
	"renderPositionMeta"
> & {
	recordingSearch?: {
		label: string;
		trackIds: readonly string[];
		onOpen: (trackId: string) => void;
	} | null;
};

export type MatchingScreenViewRecordingResult = {
	key: string;
	title: string;
	releaseTitle: string;
	duration: string;
	linkLabel: string;
	onLink: () => void;
};

export type MatchingScreenViewRecording = {
	title: string;
	queryLabel: string;
	ariaSearchLabel: string;
	query: string;
	onQueryChange: (value: string) => void;
	onSubmit: () => void;
	searchLabel: string;
	closeLabel: string;
	onClose: () => void;
	hint: string;
	results: readonly MatchingScreenViewRecordingResult[];
};

export type MatchingScreenViewFooter = {
	assignedLabel: string;
	summary: string;
	previewLabel: string;
	previewDisabled: boolean;
	onPreview: () => void;
};

export type MatchingScreenViewPreview = {
	headingRef?: Ref<HTMLHeadingElement>;
	heading: string;
	releaseTitle: string;
	summary: string;
	rows: readonly MatchingPreviewRow[];
	notice?: string;
	primary: { label: string; onPress: () => void; disabled?: boolean };
	secondary: { label: string; onPress: () => void };
};

export type MatchingScreenViewInspector = {
	ariaLabel: string;
	title: string;
	evidence: {
		fileName: string;
		position: MatchingWorkspacePosition;
		rawTags?: Record<string, string[] | null>;
	} | null;
	emptyText: string;
};

export type MatchingScreenViewWorkspace = {
	kind: "workspace";
	locale: MatchingWorkspaceLocale;
	heading: {
		backHref: string;
		backLabel: string;
		sourceDirectoryLabel: string;
		sourcePath: string;
		title: string;
		hint: string;
	};
	feedback: { text: string; visible: boolean };
	summary: { assigned: string; free: string; detail: string };
	search: ReactNode;
	preparation: ReactNode;
	createLocalReleaseHref?: string;
	board: MatchingScreenViewBoard;
	inspector: MatchingScreenViewInspector;
	recording: MatchingScreenViewRecording | null;
	footer: MatchingScreenViewFooter | null;
	preview: MatchingScreenViewPreview | null;
	onEscape: () => void;
};

export type MatchingScreenViewModel =
	| {
			kind: "receipt";
			locale: MatchingWorkspaceLocale;
			backHref: string;
			backLabel: string;
			title: string;
			body:
				| { kind: "publication"; content: ReactNode }
				| {
						kind: "links";
						releaseTitle: string;
						links: readonly MatchingScreenViewLink[];
						remaining: readonly MatchingScreenViewRemainingFile[];
						publicationDisabled: boolean;
						onOpenPublication: () => void;
						onContinue?: () => void;
						headingRef?: Ref<HTMLHeadingElement>;
						publicationButtonRef?: Ref<HTMLButtonElement>;
				  };
	  }
	| {
			kind: "missing";
			locale: MatchingWorkspaceLocale;
			backHref: string;
			backLabel: string;
			title: string;
	  }
	| MatchingScreenViewWorkspace;

export function MatchingScreenView({
	model,
}: {
	model: MatchingScreenViewModel;
}) {
	if (model.kind === "missing") {
		return (
			<section className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>{model.title}</h1>
				<a href={model.backHref}>{model.backLabel}</a>
			</section>
		);
	}

	if (model.kind === "receipt") {
		return (
			<section
				data-matching-screen
				className="mx-auto w-full max-w-[1800px] space-y-4 px-[clamp(14px,2vw,30px)] py-5 text-[var(--ink)] max-[800px]:px-3"
			>
				<header>
					<a href={model.backHref}>{model.backLabel}</a>
					<h1 className="mt-1 text-[clamp(22px,2vw,30px)] tracking-[-.025em]">
						{model.title}
					</h1>
				</header>
				{model.body.kind === "publication" ? (
					model.body.content
				) : (
					<MatchingLinkResultView
						locale={model.locale}
						releaseTitle={model.body.releaseTitle}
						links={model.body.links}
						remaining={model.body.remaining}
						headingRef={model.body.headingRef}
						publicationButtonRef={model.body.publicationButtonRef}
						publicationDisabled={model.body.publicationDisabled}
						onOpenPublication={model.body.onOpenPublication}
						onContinue={model.body.onContinue}
					/>
				)}
			</section>
		);
	}

	const { recordingSearch, ...boardProps } = model.board;
	const { inspector, recording, footer, preview } = model;

	return (
		<section
			data-matching-screen
			className="relative mx-auto w-full max-w-[1800px] px-[clamp(14px,2vw,30px)] pt-5 pb-[110px] text-[var(--ink)] max-[800px]:px-3 max-[800px]:pt-3.5 max-[800px]:pb-[120px] min-[1400px]:flex min-[1400px]:h-full min-[1400px]:min-h-0 min-[1400px]:flex-col"
			onKeyDown={(event) => {
				if (event.key === "Escape") model.onEscape();
			}}
		>
			<header className="mb-4 min-[1400px]:shrink-0">
				<p className="demo-eyebrow mb-[5px] text-[11px] text-[var(--muted)]">
					<a href={model.heading.backHref}>{model.heading.backLabel}</a> ·{" "}
					{model.heading.sourceDirectoryLabel}: {model.heading.sourcePath}
				</p>
				<h1 className="m-0 text-[clamp(22px,2vw,30px)] tracking-[-.025em]">
					{model.heading.title}
				</h1>
				<p className="mt-1 text-[var(--muted)]">{model.heading.hint}</p>
			</header>
			<div
				className="mb-2.5 h-12 min-w-0 max-[600px]:h-16 min-[1400px]:shrink-0"
				data-matching-feedback
			>
				<div
					role="status"
					aria-live="polite"
					className={`h-full overflow-auto rounded px-3 py-2 text-xs ${model.feedback.visible ? "border border-[var(--line)] bg-[var(--accent-soft)]" : ""}`}
				>
					{model.feedback.text}
				</div>
			</div>
			<div className="incoming-summary mb-3 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] px-3 py-[9px] text-[var(--muted)] min-[1400px]:shrink-0">
				<strong className="text-[var(--ink)]">{model.summary.assigned}</strong>
				<span>{model.summary.free}</span>
				<span>{model.summary.detail}</span>
			</div>
			<MatchingReviewSearchView
				locale={model.locale}
				createLocalReleaseHref={model.createLocalReleaseHref}
				search={model.search}
				preparation={model.preparation}
			/>
			<MatchingWorkspaceLayout>
				<MatchingBoard
					{...boardProps}
					renderPositionMeta={({ position }) => {
						const trackId = position.track_id;
						if (!recordingSearch || !recordingSearch.trackIds.includes(trackId))
							return null;
						return (
							<div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
								<Button onPress={() => recordingSearch.onOpen(trackId)}>
									{recordingSearch.label}
								</Button>
							</div>
						);
					}}
				/>
				<section
					data-matching-inspector
					aria-label={inspector.ariaLabel}
					className={matchingInspectorPanel}
				>
					<div className={matchingInspectorContent}>
						<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
							{inspector.title}
						</h2>
						{inspector.evidence ? (
							<>
								<p className="mt-1 text-sm wrap-anywhere">
									{inspector.evidence.fileName}
								</p>
								<MatchingEvidenceInspector
									position={inspector.evidence.position}
									rawTags={inspector.evidence.rawTags}
									locale={model.locale}
								/>
							</>
						) : (
							<p>{inspector.emptyText}</p>
						)}
					</div>
				</section>
			</MatchingWorkspaceLayout>
			{recording && (
				<section
					className={`matching-recording ${surfacePanel} mt-3 min-[1400px]:max-h-[25dvh] min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{recording.title}
					</h2>
					<form
						className="flex flex-wrap items-end gap-[7px] [&_.ui-field]:min-w-[190px] [&_.ui-field]:flex-1"
						onSubmit={(event) => {
							event.preventDefault();
							recording.onSubmit();
						}}
					>
						<label htmlFor="recording-query">{recording.queryLabel}</label>
						<TextField
							className="ui-field"
							aria-label={recording.ariaSearchLabel}
							value={recording.query}
							onChange={recording.onQueryChange}
						>
							<Input id="recording-query" />
						</TextField>
						<Button type="submit">{recording.searchLabel}</Button>
						<Button onPress={recording.onClose}>{recording.closeLabel}</Button>
					</form>
					<p>{recording.hint}</p>
					<ul>
						{recording.results.map((result) => (
							<li key={result.key}>
								{result.title} · {result.releaseTitle} · {result.duration}{" "}
								<Button onPress={result.onLink}>{result.linkLabel}</Button>
							</li>
						))}
					</ul>
				</section>
			)}
			{footer && (
				<div className="matching-commit fixed bottom-3.5 left-[calc(174px_+_18px)] right-[18px] z-[5] m-0 flex flex-wrap items-center justify-between gap-x-5 gap-y-2 rounded-md border border-[var(--line)] bg-[color-mix(in_srgb,var(--surface)_95%,transparent)] px-3.5 py-2.5 shadow-[0_6px_24px_#0002] backdrop-blur-[12px] max-[800px]:right-2 max-[800px]:bottom-2 max-[800px]:left-2 max-[800px]:p-2 max-[520px]:items-stretch">
					<strong>{footer.assignedLabel}</strong>
					<span className="min-w-[220px] flex-1 text-[11px] text-[var(--muted)] max-[800px]:hidden">
						{footer.summary}
					</span>
					<Button
						className="demo-button max-[520px]:w-full"
						isDisabled={footer.previewDisabled}
						onPress={footer.onPreview}
					>
						{footer.previewLabel}
					</Button>
				</div>
			)}
			{preview && (
				<MatchingPreview
					locale={model.locale}
					headingRef={preview.headingRef}
					heading={preview.heading}
					releaseTitle={preview.releaseTitle}
					summary={preview.summary}
					rows={preview.rows}
					notice={preview.notice}
					primary={preview.primary}
					secondary={preview.secondary}
				/>
			)}
		</section>
	);
}
