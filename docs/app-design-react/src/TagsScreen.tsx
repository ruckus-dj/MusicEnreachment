import type {
	CatalogArtist,
	OriginalRelease,
	SourceFiles,
} from "./catalogData";
import { TagsComparison, type TagScope } from "./TagsComparison";
import { useI18n } from "./i18n/I18nProvider";
import { trackMessage } from "./trackMessages";

export function TagsScreen({
	trackId,
	scope = "track",
	releases,
	artists,
	files,
	onSelectFile,
	onPreview,
	fixedContext = false,
}: {
	trackId: string;
	scope?: TagScope;
	releases: OriginalRelease[];
	artists: CatalogArtist[];
	files: SourceFiles;
	onSelectFile?: (trackId: string, fileId: string) => void;
	onPreview?: (ids: string[]) => void;
	fixedContext?: boolean;
}) {
	const { locale } = useI18n();
	return (
		<section className="catalogue-object min-w-0">
			<header className="mb-3 flex flex-wrap items-center justify-between gap-3">
				<div className="min-w-0 [overflow-wrap:anywhere]">
					<h1>{trackMessage(locale, "tagsTitle")}</h1>
					<p className="text-[var(--muted)]">
						{trackMessage(locale, "tagsDescription")}
					</p>
				</div>
				{!fixedContext && (
					<a className="text-[var(--accent)] hover:underline" href="#library">
						{trackMessage(locale, "library")}
					</a>
				)}
			</header>
			<section
				className="min-w-0"
				aria-label={
					fixedContext ? trackMessage(locale, "tagsTitle") : undefined
				}
				aria-labelledby={!fixedContext ? "tag-context-title" : undefined}
			>
				{!fixedContext && (
					<h2 id="tag-context-title">
						{trackMessage(locale, "comparisonContext")}
					</h2>
				)}
				<TagsComparison
					releases={releases}
					artists={artists}
					files={files}
					scope={scope}
					initialTrackId={trackId}
					onSelectFile={onSelectFile}
					onPreview={onPreview}
					allowScopeChange={!fixedContext}
					fixedContext={fixedContext}
				/>
			</section>
		</section>
	);
}
