import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { genreCatalog, resolveCatalogGenreName } from "../genres/catalogue";
import type {
	GenreOption,
	GenrePickerRow,
	GenrePickerSelection,
	GenrePickerViewProps,
	GenreReference,
} from "./GenrePickerView";

export type GenrePickerProps = {
	value: GenreReference[];
	onChange: (next: GenreReference[]) => void;
	readOnly?: boolean;
	loaded: boolean;
	onLoad?: () => void | Promise<void>;
	displayValue?: string;
	label?: string;
	options?: readonly GenreOption[];
};

const pageSize = 60;

/**
 * Headless genre picker model for the prototype. It owns the bundled catalog
 * default, catalog identity/name canonicalization, search ranking, selection
 * retention and the catalog load state, then projects render-ready view props.
 * Open/focus/Escape stay in the view.
 */
export function useGenrePickerDemoProvider(
	{
		value,
		onChange,
		readOnly = false,
		loaded,
		onLoad,
		displayValue,
		label,
		options = genreCatalog,
	}: GenrePickerProps,
	lifetime = "picker",
): GenrePickerViewProps {
	const [query, setQuery] = useState("");
	const [visibleCount, setVisibleCount] = useState(pageSize);
	const [loading, setLoading] = useState(false);
	const [loadError, setLoadError] = useState(false);
	const loadGeneration = useRef(0);
	useEffect(() => {
		setQuery("");
		setVisibleCount(pageSize);
		setLoading(false);
		setLoadError(false);
		return () => {
			loadGeneration.current++;
		};
	}, [lifetime]);

	const catalog = useMemo(
		() => new Map(options.map((item) => [item.id, item])),
		[options],
	);
	const catalogId = useCallback(
		(item: GenreReference): string | undefined =>
			item.id ??
			options.find(
				(option) =>
					resolveCatalogGenreName(option) === resolveCatalogGenreName(item),
			)?.id,
		[options],
	);
	const selectedKeys = useMemo(() => {
		const keys = value.flatMap((item) => {
			return [item.id ?? item.name, catalogId(item)].filter(
				(key): key is string => Boolean(key),
			);
		});
		return new Set(keys);
	}, [catalogId, value]);

	const search = query.trim().toLocaleLowerCase("en");
	const filtered = useMemo(() => {
		const matches = options.filter((item) =>
			item.name.toLocaleLowerCase("en").includes(search),
		);
		// Exact and prefix matches must not disappear behind a page of subgenres.
		if (!search) return matches;
		const rank = (name: string) => {
			const normalized = name.toLocaleLowerCase("en");
			return normalized === search ? 0 : normalized.startsWith(search) ? 1 : 2;
		};
		return matches.sort((left, right) => rank(left.name) - rank(right.name));
	}, [options, search]);
	const shownOptions = filtered.slice(0, visibleCount);
	const visibleKeys = new Set(shownOptions.map((item) => item.id));

	const rows: readonly GenrePickerRow[] = shownOptions.map((item) => ({
		id: item.id,
		name: item.name,
		label: resolveCatalogGenreName(item),
	}));
	const selected: readonly GenrePickerSelection[] = value.map((item) => ({
		key: item.id ?? item.name,
		name: resolveCatalogGenreName(item),
	}));
	const visibleSelectedKeys = new Set(
		Array.from(selectedKeys).filter((key) => visibleKeys.has(key)),
	);

	const remove = (key: string) =>
		onChange(
			value
				.filter((item) => (item.id ?? item.name) !== key)
				.map((item) => ({ ...item, name: resolveCatalogGenreName(item) })),
		);
	const load = async () => {
		if (!onLoad || loading) return;
		const generation = loadGeneration.current;
		setLoading(true);
		setLoadError(false);
		try {
			await onLoad();
		} catch {
			if (loadGeneration.current === generation) setLoadError(true);
		} finally {
			if (loadGeneration.current === generation) setLoading(false);
		}
	};
	const changeQuery = (next: string) => {
		setQuery(next);
		setVisibleCount(pageSize);
	};
	const changeSelection = (keys: "all" | readonly string[]) => {
		const nextKeys =
			keys === "all" ? shownOptions.map((item) => item.id) : keys;
		const retained = value
			.filter((item) => {
				const key = catalogId(item) ?? item.name;
				return !catalog.has(key) || !visibleKeys.has(key);
			})
			.map((item) => ({
				...item,
				name: resolveCatalogGenreName(item),
			}));
		const next = filtered
			.filter((item) => nextKeys.includes(item.id))
			.map((item) => ({
				id: item.id,
				name: resolveCatalogGenreName(item),
			}));
		onChange([...retained, ...next]);
	};

	return {
		label,
		displayValue,
		readOnly,
		loaded,
		canLoad: Boolean(onLoad),
		loading,
		loadError,
		query,
		catalogEmpty: options.length === 0,
		rows,
		selected,
		visibleSelectedKeys,
		remainingCount: filtered.length - shownOptions.length,
		onQueryChange: changeQuery,
		onLoad: () => void load(),
		onRemove: remove,
		onShowMore: () => setVisibleCount((count) => count + pageSize),
		onSelectionChange: changeSelection,
		onResetSearch: () => {
			setQuery("");
			setVisibleCount(pageSize);
		},
		onResetQuery: () => setQuery(""),
	};
}
