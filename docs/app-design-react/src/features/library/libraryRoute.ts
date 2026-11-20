import type { CatalogFilter } from "./availabilityRows";

export const LIBRARY_PAGE_SIZE = 50;

export type LibraryView = "artists" | "albums" | "tracks";

export interface LibraryRouteState {
	view: LibraryView;
	query: string;
	filter: CatalogFilter;
	page: number;
}

const views: readonly LibraryView[] = ["artists", "albums", "tracks"];
const filters: readonly CatalogFilter[] = [
	"all",
	"missing",
	"available",
	"published",
];

const isView = (value: string | null): value is LibraryView =>
	views.includes(value as LibraryView);
const isFilter = (value: string | null): value is CatalogFilter =>
	filters.includes(value as CatalogFilter);

const positivePage = (value: string | null) => {
	const page = Number.parseInt(value ?? "", 10);
	return Number.isFinite(page) && page > 0 ? page : 1;
};

export const defaultLibraryRouteState: LibraryRouteState = {
	view: "artists",
	query: "",
	filter: "all",
	page: 1,
};

/** Reads the paged library state from its hash route; other routes use defaults. */
export function parseLibraryRoute(route: string): LibraryRouteState {
	const [path, search = ""] = route.split("?");
	if (path !== "library") return defaultLibraryRouteState;
	const params = new URLSearchParams(search);
	const requestedView = params.get("tab") ?? params.get("view");
	return {
		view: isView(requestedView) ? requestedView : defaultLibraryRouteState.view,
		query: params.get("q") ?? "",
		filter: isFilter(params.get("filter"))
			? (params.get("filter") as CatalogFilter)
			: defaultLibraryRouteState.filter,
		page: positivePage(params.get("page")),
	};
}

/** Produces the canonical `library` hash route without the leading `#`. */
export function buildLibraryRouteHref(state: LibraryRouteState): string {
	const params = new URLSearchParams();
	params.set("tab", state.view);
	if (state.query) params.set("q", state.query);
	params.set("filter", state.filter);
	if (state.page > 1) params.set("page", String(state.page));
	return `library?${params.toString()}`;
}
