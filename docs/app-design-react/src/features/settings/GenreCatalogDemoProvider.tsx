import { useState } from "react";
import { genreCatalog } from "../../genres/catalogue";
import { useI18n } from "../../i18n/I18nProvider";
import { GenreCatalogView } from "./GenreCatalogView";

export function GenreCatalogDemoProvider({
	loaded,
	onRefresh,
}: {
	loaded: boolean;
	onRefresh: () => void;
}) {
	const { locale } = useI18n();
	const [lastRefreshedAt, setLastRefreshedAt] = useState<string>();
	return (
		<GenreCatalogView
			locale={locale}
			items={genreCatalog}
			status={{
				count: genreCatalog.length,
				available: loaded,
				updatedAt: lastRefreshedAt ?? null,
				attemptedAt: lastRefreshedAt ?? null,
			}}
			refreshState="idle"
			onRefresh={() => {
				setLastRefreshedAt(new Date().toISOString());
				onRefresh();
			}}
		/>
	);
}
