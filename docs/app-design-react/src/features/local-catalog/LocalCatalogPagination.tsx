import { Button } from "react-aria-components";
import { secondary } from "../local-release/localReleaseUi";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";

export function LocalCatalogPagination({
	locale,
	page,
	totalPages,
	totalItems,
	onPageChange,
}: {
	locale: "ru" | "en";
	page: number;
	totalPages: number;
	totalItems: number;
	onPageChange: (page: number) => void;
}) {
	const m = (
		key: LocalCatalogMessage,
		values?: Record<string, string | number>,
	) => localCatalogMessage(locale, key, values);
	return (
		<nav
			className="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] pt-3 text-xs"
			aria-label={m("pages")}
		>
			<span role="status" className="text-[var(--muted)]">
				{m("pageStatus", { page, pages: totalPages })} ·{" "}
				{m("totalItems", { count: totalItems })}
			</span>
			<div className="flex gap-2">
				<Button
					className={secondary}
					isDisabled={page <= 1}
					onPress={() => onPageChange(page - 1)}
				>
					{m("previousPage")}
				</Button>
				<Button
					className={secondary}
					isDisabled={page >= totalPages}
					onPress={() => onPageChange(page + 1)}
				>
					{m("nextPage")}
				</Button>
			</div>
		</nav>
	);
}
