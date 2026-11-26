import { Button } from "react-aria-components";
import { secondary } from "../local-release/localReleaseUi";

export function OwnCatalogPagination({
	locale,
	page,
	totalPages,
	totalItems,
	busy,
	onPageChange,
}: {
	locale: "ru" | "en";
	page: number;
	totalPages: number;
	totalItems: number;
	busy?: boolean;
	onPageChange: (page: number) => void;
}) {
	const copy =
		locale === "ru"
			? {
					pages: "Страницы собственного каталога",
					status: `Страница ${page} из ${totalPages} · Всего: ${totalItems}`,
					previous: "Предыдущая страница",
					next: "Следующая страница",
				}
			: {
					pages: "Own catalog pages",
					status: `Page ${page} of ${totalPages} · Total: ${totalItems}`,
					previous: "Previous page",
					next: "Next page",
				};
	return (
		<nav
			className="mt-2 flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] pt-4 text-sm"
			aria-label={copy.pages}
		>
			<span role="status" className="text-[var(--muted)]">
				{copy.status}
			</span>
			<div className="flex gap-2">
				<Button
					className={secondary}
					isDisabled={Boolean(busy) || page <= 1}
					onPress={() => onPageChange(page - 1)}
				>
					{copy.previous}
				</Button>
				<Button
					className={secondary}
					isDisabled={Boolean(busy) || page >= totalPages}
					onPress={() => onPageChange(page + 1)}
				>
					{copy.next}
				</Button>
			</div>
		</nav>
	);
}
