import { useI18n } from "../../i18n/I18nProvider";
import { LibraryPaginationView } from "./LibraryPaginationView";

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
	return (
		<LibraryPaginationView
			locale={locale}
			page={page}
			totalPages={totalPages}
			totalItems={totalItems}
			hasPrevious={page > 1}
			hasNext={page < totalPages}
			onPageChange={onPageChange}
		/>
	);
}
