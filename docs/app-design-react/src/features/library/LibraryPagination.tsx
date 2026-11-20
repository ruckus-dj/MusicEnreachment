import { useI18n } from "../../i18n/I18nProvider";
import { Button } from "../../components/ui";
import {
	libraryPaginationMessage,
	type LibraryPaginationMessage,
} from "./libraryPaginationMessages";

export function LibraryPagination({
	page,
	totalPages,
	totalItems,
	onPageChange,
}: {
	page: number;
	totalPages: number;
	totalItems: number;
	onPageChange: (page: number) => void;
}) {
	const { locale } = useI18n();
	const m = (
		key: LibraryPaginationMessage,
		values?: Record<string, string | number>,
	) => libraryPaginationMessage(locale, key, values);
	return (
		<nav
			className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs"
			aria-label={m("pages")}
		>
			<span role="status" className="text-[var(--muted)]">
				{m("pageStatus", { page, pages: totalPages })} ·{" "}
				{m("totalItems", { count: totalItems })}
			</span>
			<div className="flex gap-2">
				<Button isDisabled={page <= 1} onPress={() => onPageChange(page - 1)}>
					{m("previousPage")}
				</Button>
				<Button
					isDisabled={page >= totalPages}
					onPress={() => onPageChange(page + 1)}
				>
					{m("nextPage")}
				</Button>
			</div>
		</nav>
	);
}
