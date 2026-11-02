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
		<section className="catalogue-object">
			<header className="demo-object-header">
				<div>
					<h1>Сравнение тегов</h1>
					<p>
						Теги источника, кэша провайдера, локальной медиатеки и отдельного
						снимка публикации показаны раздельно.
					</p>
				</div>
				<a href="#library">К медиатеке</a>
			</header>
			<section aria-labelledby="tag-context-title">
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
