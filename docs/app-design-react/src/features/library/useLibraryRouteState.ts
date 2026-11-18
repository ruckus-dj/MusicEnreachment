import { useCallback, useEffect, useRef, useState } from "react";
import type { CatalogFilter } from "../../availabilityRows";
import {
	buildLibraryRouteHref,
	parseLibraryRoute,
	type LibraryRouteState,
	type LibraryView,
} from "./libraryRoute";

export const LIBRARY_SEARCH_DEBOUNCE_MS = 250;

/**
 * Keeps the paged library controls and the hash route in sync. Search input is
 * immediate while the effective query, page and URL settle after the debounce.
 */
export function useLibraryRouteState(route: string) {
	const [state, setState] = useState<LibraryRouteState>(() =>
		parseLibraryRoute(route),
	);
	const [searchInput, setSearchInput] = useState(
		() => parseLibraryRoute(route).query,
	);
	const lastHref = useRef(buildLibraryRouteHref(state));
	const pendingSearch = useRef<number | null>(null);
	const skipNextUrlWrite = useRef(false);
	const isLibraryRoute = route.split("?")[0] === "library";

	const clearPendingSearch = useCallback(() => {
		if (pendingSearch.current !== null) {
			window.clearTimeout(pendingSearch.current);
			pendingSearch.current = null;
		}
	}, []);

	// Adopt browser back/forward or a copied link without fighting our own writes.
	useEffect(() => {
		if (!isLibraryRoute) return;
		const next = parseLibraryRoute(route);
		const href = buildLibraryRouteHref(next);
		if (route === lastHref.current || href === lastHref.current) return;
		lastHref.current = href;
		skipNextUrlWrite.current = true;
		clearPendingSearch();
		setSearchInput(next.query);
		setState(next);
	}, [route, isLibraryRoute, clearPendingSearch]);

	useEffect(() => {
		if (isLibraryRoute) return;
		clearPendingSearch();
		setSearchInput(state.query);
	}, [isLibraryRoute, state.query, clearPendingSearch]);

	useEffect(() => {
		if (!isLibraryRoute) return;
		if (skipNextUrlWrite.current) {
			skipNextUrlWrite.current = false;
			return;
		}
		const href = buildLibraryRouteHref(state);
		if (href === lastHref.current) return;
		lastHref.current = href;
		window.location.hash = href;
	}, [state, isLibraryRoute]);

	useEffect(
		() => () => {
			clearPendingSearch();
		},
		[clearPendingSearch],
	);

	const setView = useCallback((view: LibraryView) => {
		setState((current) =>
			current.view === view ? current : { ...current, view, page: 1 },
		);
	}, []);

	const setFilter = useCallback((filter: CatalogFilter) => {
		setState((current) =>
			current.filter === filter ? current : { ...current, filter, page: 1 },
		);
	}, []);

	const setPage = useCallback(
		(page: number, totalPages = Number.MAX_SAFE_INTEGER) => {
			const nextPage = Math.max(
				1,
				Math.min(Math.trunc(page), Math.max(1, totalPages)),
			);
			setState((current) =>
				current.page === nextPage ? current : { ...current, page: nextPage },
			);
		},
		[],
	);

	const setSearch = useCallback(
		(value: string) => {
			setSearchInput(value);
			clearPendingSearch();
			pendingSearch.current = window.setTimeout(() => {
				pendingSearch.current = null;
				setState((current) =>
					current.query === value
						? current
						: { ...current, query: value, page: 1 },
				);
			}, LIBRARY_SEARCH_DEBOUNCE_MS);
		},
		[clearPendingSearch],
	);

	return {
		view: state.view,
		query: state.query,
		filter: state.filter,
		page: state.page,
		searchInput,
		isLibraryRoute,
		setView,
		setFilter,
		setPage,
		setSearch,
	};
}
