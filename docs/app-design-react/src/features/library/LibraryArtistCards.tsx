import { Checkbox, Link } from "react-aria-components";
import { catalogueStyles } from "./catalogueStyles";

const copyByLocale = {
	en: {
		artistsLibrary: "Library artists",
		performer: "Performer",
		releases: "Releases",
		positions: "Positions",
		audio: "Audio",
		published: "Published",
		missingEditions: "Without an edition",
		selectArtist: (name: string) => `Select positions by ${name}`,
		selectPage: "Select on this page",
		noArtists: "No artists found.",
		partialProviderScope: "Provider counts cover the known cache only.",
	},
	ru: {
		artistsLibrary: "Артисты медиатеки",
		performer: "Исполнитель",
		releases: "Изданий",
		positions: "Позиций",
		audio: "Аудио",
		published: "Опубликовано",
		missingEditions: "Без издания",
		selectArtist: (name: string) => `Выбрать позиции артиста ${name}`,
		selectPage: "Выбрать на этой странице",
		noArtists: "Артисты не найдены.",
		partialProviderScope: "Данные провайдера ограничены известным кэшем.",
	},
} as const;

const checkboxFocus =
	"data-[disabled]:cursor-default data-[disabled]:opacity-45 data-[focus-visible]:outline-2 data-[focus-visible]:outline-offset-2 data-[focus-visible]:outline-[var(--focus)]";

/** Display data for one artist card; counts are resolved by the host. */
export interface LibraryArtistCard {
	id: string;
	name: string;
	href: string;
	monogram: string;
	palette: string;
	genres: string[];
	releaseCount: number;
	/** Undefined when the provider does not expose missing-edition data; never inferred as 0. */
	missingEditionCount?: number;
	positionCount: number;
	availableCount: number;
	publishedCount: number;
	partialProviderScope?: boolean;
	/** Plain per-card selection state and action, derived by the host. */
	selection: ArtistCardSelectionControl;
}

/** Raw page-scoped selection supplied by hosts that allow selecting card positions. */
export interface ArtistSelectionState {
	selectedIds: ReadonlySet<string>;
	onChange: (ids: string[], selected: boolean) => void;
}

/** Per-card or page-wide selection state and action; the card view owns the markup. */
export interface ArtistCardSelectionControl {
	isSelected: boolean;
	isIndeterminate: boolean;
	isDisabled: boolean;
	onChange: (selected: boolean) => void;
}

interface Props {
	locale: "en" | "ru";
	artists: LibraryArtistCard[];
	/** Optional page-wide select; hosts without one render cards only. */
	pageSelectionControl?: ArtistCardSelectionControl;
}

const selectionMark = (control: ArtistCardSelectionControl) =>
	control.isIndeterminate ? "−" : control.isSelected ? "✓" : "";

const pageSelectionRow =
	"flex items-center gap-2 px-[clamp(12px,1.5vw,20px)] pt-[clamp(12px,1.5vw,20px)] text-xs text-[var(--muted)]";

export function LibraryArtistCards({
	locale,
	artists,
	pageSelectionControl,
}: Props) {
	const copy = copyByLocale[locale];
	return (
		<>
			{pageSelectionControl ? (
				<div className={pageSelectionRow}>
					<Checkbox
						slot={null}
						className={`${catalogueStyles.tableCheckbox} ${checkboxFocus}`}
						aria-label={copy.selectPage}
						isSelected={pageSelectionControl.isSelected}
						isIndeterminate={pageSelectionControl.isIndeterminate}
						isDisabled={pageSelectionControl.isDisabled}
						onChange={pageSelectionControl.onChange}
					>
						<span className={catalogueStyles.checkboxBox} aria-hidden="true">
							{selectionMark(pageSelectionControl)}
						</span>
					</Checkbox>
					<span>{copy.selectPage}</span>
				</div>
			) : null}
			<section
				className={catalogueStyles.artistGrid}
				aria-label={copy.artistsLibrary}
			>
				{artists.length ? (
					artists.map((artist) => (
						<article className={catalogueStyles.artistCard} key={artist.id}>
							<div
								className={catalogueStyles.artistArt}
								data-palette={artist.palette}
							>
								<Link
									href={artist.href}
									className={catalogueStyles.artistCardLink}
								>
									<span
										className={catalogueStyles.artistMonogram}
										aria-hidden="true"
									>
										{artist.monogram}
									</span>
									<span className={catalogueStyles.artistOverlay}>
										<span className={catalogueStyles.artistCopy}>
											<strong>{artist.name}</strong>
											<span>{artist.genres.join(" · ") || copy.performer}</span>
										</span>
										<span className={catalogueStyles.artistStats}>
											<span>
												<strong>
													{copy.releases}: {artist.releaseCount}
												</strong>
											</span>
											<span>
												<strong>
													{copy.positions}: {artist.positionCount}
												</strong>
											</span>
											<span>
												<strong>
													{copy.audio}: {artist.availableCount} /{" "}
													{artist.positionCount}
												</strong>
											</span>
											<span>
												<strong>
													{copy.published}: {artist.publishedCount} /{" "}
													{artist.positionCount}
												</strong>
											</span>
											{artist.missingEditionCount !== undefined && (
												<span>
													<strong>
														{copy.missingEditions}:{" "}
														{artist.missingEditionCount || "—"}
													</strong>
												</span>
											)}
											{artist.partialProviderScope && (
												<span className="col-span-2">
													{copy.partialProviderScope}
												</span>
											)}
										</span>
									</span>
								</Link>
								<div className={catalogueStyles.cardSelection}>
									<Checkbox
										slot={null}
										className={`${catalogueStyles.cardCheckbox} ${checkboxFocus}`}
										aria-label={copy.selectArtist(artist.name)}
										isSelected={artist.selection.isSelected}
										isIndeterminate={artist.selection.isIndeterminate}
										isDisabled={artist.selection.isDisabled}
										onChange={artist.selection.onChange}
									>
										<span
											className={catalogueStyles.cardCheckboxBox}
											aria-hidden="true"
										>
											{selectionMark(artist.selection)}
										</span>
									</Checkbox>
								</div>
							</div>
						</article>
					))
				) : (
					<div className={catalogueStyles.empty}>{copy.noArtists}</div>
				)}
			</section>
		</>
	);
}
