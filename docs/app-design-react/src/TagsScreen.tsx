import type {
	CatalogArtist,
	OriginalRelease,
	SourceFiles,
} from "./catalogData";
import { TagsComparison, type TagScope } from "./TagsComparison";

export function TagsScreen({
	trackId,
	scope = "track",
	releases,
	artists,
	files,
	onSelectFile,
	onPreview,
}: {
	trackId: string;
	scope?: TagScope;
	releases: OriginalRelease[];
	artists: CatalogArtist[];
	files: SourceFiles;
	onSelectFile?: (trackId: string, fileId: string) => void;
	onPreview?: (ids: string[]) => void;
}) {
	return (
		<section className="catalogue-object min-w-0">
			<header className="mb-3 flex flex-wrap items-center justify-between gap-3">
				<div className="min-w-0 [overflow-wrap:anywhere]">
					<h1>Сравнение тегов</h1>
					<p className="text-[var(--muted)]">
						Теги источника, кэша провайдера, локальной медиатеки и отдельного
						снимка публикации показаны раздельно.
					</p>
				</div>
				<a className="text-[var(--accent)] hover:underline" href="#library">
					К медиатеке
				</a>
			</header>
			<section className="min-w-0" aria-labelledby="tag-context-title">
				<h2 id="tag-context-title">Контекст сравнения</h2>
				<TagsComparison
					releases={releases}
					artists={artists}
					files={files}
					scope={scope}
					initialTrackId={trackId}
					onSelectFile={onSelectFile}
					onPreview={onPreview}
					allowScopeChange
				/>
			</section>
		</section>
	);
}
