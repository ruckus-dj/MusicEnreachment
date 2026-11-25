import type { ComponentProps, ReactNode } from "react";
import { Button } from "../../components/ui";
import type { Locale } from "../../i18n/types";
import { catalogueStyles } from "./catalogueStyles";
import {
	LibraryPositionHeadingView,
	libraryPositionPageClassName,
	libraryPositionTitleId,
} from "./LibraryPositionHeadingView";
import { LibraryPositionIdentityView } from "./LibraryPositionIdentityView";
import {
	LibraryPositionSourcesView,
	type LibraryPositionSource,
} from "./LibraryPositionSourcesView";
import { LibraryPositionTagsDisclosure } from "./LibraryPositionTagsDisclosure";
import { LibraryPositionTechnicalDetailsView } from "./LibraryPositionTechnicalDetailsView";
import {
	LibraryPositionPreviewButton,
	LibraryPositionWorkspace,
} from "./LibraryPositionWorkspace";
import { PublicationCurrentDetailsView } from "./PublicationCurrentSummaryView";
import { PublicationTagSnapshotView } from "./PublicationTagSnapshotView";
import { trackMessage } from "./trackMessages";

/**
 * Pure track screen composition. Derived publication state, provider lookup and
 * nested domain actors (metadata editors, track actions, tag comparison) are
 * supplied by the demo provider; this view only renders the given models.
 */
export type TrackScreenViewProps = {
	locale: Locale;
	heading: {
		displayNumber: string;
		title: string;
		artist?: string;
		releaseTitle: string;
		releaseHref?: string;
		libraryHref: string;
	};
	editors?: ReactNode;
	sources: {
		position: string;
		items: readonly LibraryPositionSource[];
		choice: { value: string; onChange: (id: string) => void };
		inspection: { value?: string; onChange: (id: string) => void };
	};
	publication: {
		status: string;
		diff?: ReactNode;
		outputStatus?: string;
		onPreview: () => void;
		onUpdateMetadata?: () => void;
		preview?: ReactNode;
	};
	actions?: ReactNode;
	technical: {
		position: string;
		title: string;
		durationMs: number | null;
		recordingTitle: string;
		fileState: string;
		sourceFormat?: string;
		path?: string;
		composition?: string;
		providerHint: string;
		externalChangeTime?: string;
		current?: {
			details: ComponentProps<typeof PublicationCurrentDetailsView>["details"];
			tags: ComponentProps<typeof PublicationTagSnapshotView>["tags"];
		};
	};
	tags: {
		open: boolean;
		onOpenChange: (open: boolean) => void;
		comparison: ReactNode;
	};
};

export function TrackScreenView({
	locale,
	heading,
	editors,
	sources,
	publication,
	actions,
	technical,
	tags,
}: TrackScreenViewProps) {
	return (
		<section
			aria-labelledby={libraryPositionTitleId}
			className={libraryPositionPageClassName}
		>
			<LibraryPositionHeadingView
				locale={locale}
				displayNumber={heading.displayNumber}
				title={heading.title}
				artist={heading.artist}
				releaseTitle={heading.releaseTitle}
				releaseHref={heading.releaseHref}
				libraryHref={heading.libraryHref}
			/>
			<LibraryPositionWorkspace
				locale={locale}
				editors={editors}
				sources={
					<LibraryPositionSourcesView
						locale={locale}
						position={sources.position}
						sources={sources.items}
						choice={sources.choice}
						inspection={sources.inspection}
					/>
				}
				publication={
					<>
						<p aria-label={trackMessage(locale, "metadataState")}>
							<strong>{publication.status}</strong>
						</p>
						{publication.diff && (
							<details>
								<summary>
									{trackMessage(locale, "compareEffectiveMetadata")}
								</summary>
								{publication.diff}
							</details>
						)}
						{publication.outputStatus && (
							<p>
								{trackMessage(locale, "publicationFileStatus")}:{" "}
								{publication.outputStatus}.
							</p>
						)}
					</>
				}
				actions={
					<>
						<div className={catalogueStyles.actions}>
							{publication.onUpdateMetadata && (
								<Button onPress={publication.onUpdateMetadata}>
									{trackMessage(locale, "updateMetadata")}
								</Button>
							)}
							<LibraryPositionPreviewButton
								locale={locale}
								onPreview={publication.onPreview}
							/>
						</div>
						{publication.preview}
						{actions}
					</>
				}
				technicalDetails={
					<LibraryPositionTechnicalDetailsView
						locale={locale}
						current={
							technical.current && (
								<>
									<PublicationCurrentDetailsView
										locale={locale}
										details={technical.current.details}
									/>
									<PublicationTagSnapshotView
										locale={locale}
										tags={technical.current.tags}
									/>
								</>
							)
						}
						afterCurrent={<p>{trackMessage(locale, "localChangesHelp")}</p>}
					>
						<LibraryPositionIdentityView
							locale={locale}
							position={technical.position}
							title={technical.title}
							durationMs={technical.durationMs}
							recordingTitle={technical.recordingTitle}
						/>
						<dl className={catalogueStyles.facts}>
							<dt>{trackMessage(locale, "fileState")}</dt>
							<dd>{technical.fileState}</dd>
							<dt>{trackMessage(locale, "sourceFormat")}</dt>
							<dd>
								{technical.sourceFormat ?? trackMessage(locale, "notSelected")}
							</dd>
						</dl>
						<dl className={catalogueStyles.facts}>
							<dt>{trackMessage(locale, "path")}</dt>
							<dd>{technical.path ?? trackMessage(locale, "notSelected")}</dd>
							<dt>{trackMessage(locale, "composition")}</dt>
							<dd>{technical.composition ?? trackMessage(locale, "noLink")}</dd>
							<dt>{trackMessage(locale, "providerHint")}</dt>
							<dd>{technical.providerHint}</dd>
							{technical.externalChangeTime && (
								<>
									<dt>{trackMessage(locale, "externalChangeTime")}</dt>
									<dd>{technical.externalChangeTime}</dd>
								</>
							)}
						</dl>
					</LibraryPositionTechnicalDetailsView>
				}
			/>
			<LibraryPositionTagsDisclosure
				locale={locale}
				open={tags.open}
				onOpenChange={tags.onOpenChange}
			>
				{tags.comparison}
			</LibraryPositionTagsDisclosure>
		</section>
	);
}
