import type { LibraryView } from "../../../ui/features/library/libraryView";
import type { CatalogFilter } from "./availabilityRows";

export type { LibraryView } from "../../../ui/features/library/libraryView";

export const LIBRARY_PAGE_SIZE = 50;

export type PublicationFilter = "all" | "published" | "unpublished";
export type AvailabilityFilter = "all" | "available" | "missing";

export interface LibraryRouteState {
	view: LibraryView;
	query: string;
	publication: PublicationFilter;
	availability: AvailabilityFilter;
	offset: number;
}

const views: readonly LibraryView[] = ["artists", "albums", "tracks"];
const publications: readonly PublicationFilter[] = [
	"all",
	"published",
	"unpublished",
];
const availabilities: readonly AvailabilityFilter[] = [
	"all",
	"available",
	"missing",
];

const isView = (value: string | null): value is LibraryView =>
	views.includes(value as LibraryView);
const isPublication = (value: string | null): value is PublicationFilter =>
	publications.includes(value as PublicationFilter);
const isAvailability = (value: string | null): value is AvailabilityFilter =>
	availabilities.includes(value as AvailabilityFilter);

const positiveOffset = (value: string | null) => {
	const offset = Number(value ?? 0);
	return Number.isSafeInteger(offset) && offset > 0 ? offset : 0;
};

export const defaultLibraryRouteState: LibraryRouteState = {
	view: "artists",
	query: "",
	publication: "all",
	availability: "all",
	offset: 0,
};

/** Collapses the two canonical filter axes into the single visible filter. */
export function libraryFilterFor(
	state: Pick<LibraryRouteState, "publication" | "availability">,
): CatalogFilter {
	if (state.publication === "published") return "published";
	if (state.availability === "available") return "available";
	if (state.availability === "missing") return "missing";
	return "all";
}

/** Applies the single visible filter to both canonical filter axes. */
export function withLibraryFilter(
	state: LibraryRouteState,
	filter: CatalogFilter,
): LibraryRouteState {
	return {
		...state,
		publication: filter === "published" ? "published" : "all",
		availability:
			filter === "available"
				? "available"
				: filter === "missing"
					? "missing"
					: "all",
	};
}

/** Reads the canonical paged library state; other routes use defaults. */
export function parseLibraryRoute(route: string): LibraryRouteState {
	const path = route.startsWith("#") ? route.slice(1) : route;
	const [pathname, search = ""] = path.split("?", 2);
	if (pathname !== "/library") return defaultLibraryRouteState;
	const params = new URLSearchParams(search);
	const requestedView = params.get("view");
	const requestedPublication = params.get("publication");
	const requestedAvailability = params.get("availability");
	const parsed: LibraryRouteState = {
		view: isView(requestedView) ? requestedView : defaultLibraryRouteState.view,
		query: params.get("q") ?? "",
		publication: isPublication(requestedPublication)
			? requestedPublication
			: "all",
		availability: isAvailability(requestedAvailability)
			? requestedAvailability
			: "all",
		offset: positiveOffset(params.get("offset")),
	};
	return parsed;
}

/** Produces the canonical `#/library` hash route. */
export function buildLibraryRouteHref(state: LibraryRouteState): string {
	const params = new URLSearchParams();
	params.set("view", state.view);
	if (state.query) params.set("q", state.query);
	params.set("publication", state.publication);
	params.set("availability", state.availability);
	if (state.offset > 0) params.set("offset", String(state.offset));
	return `#/library?${params.toString()}`;
}

/** Canonical root-list destination for links from detail and preview screens. */
export function libraryRootHref(): string {
	return "#/library";
}
