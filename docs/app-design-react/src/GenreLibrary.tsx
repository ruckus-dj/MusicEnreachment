import { Button, StatusBadge } from "./ui";
import { formStyles as styles } from "./formStyles";

export const demoGenres = [
	{ id: "demo-genre-ambient", name: "ambient" },
	{ id: "demo-genre-folk", name: "folk" },
	{ id: "demo-genre-contemporary-jazz", name: "contemporary jazz" },
	{ id: "demo-genre-electronic", name: "electronic" },
	{ id: "demo-genre-rock", name: "rock" },
	{ id: "demo-genre-soul", name: "soul" },
	{ id: "demo-genre-classical", name: "classical" },
	{ id: "demo-genre-hip-hop", name: "hip hop" },
] as const;

export function GenreLibrary({
	loaded,
	onLoadedChange,
	buttonLabel = "Загрузить каталог в демо",
}: {
	loaded: boolean;
	onLoadedChange: (loaded: boolean) => void;
	buttonLabel?: string;
}) {
	return (
		<div className={styles.genreLibrary}>
			<div className={styles.genreLibraryStatus}>
				<strong>
					{loaded ? "Каталог доступен в демонстрации" : "Каталог недоступен"}
				</strong>
				<StatusBadge tone={loaded ? "success" : "warning"}>
					{loaded ? "Доступен" : "Не загружен"}
				</StatusBadge>
				<p>
					{loaded
						? "Последнее успешное обновление: демо · сейчас."
						: "Жанры не загружены. Выбор жанров недоступен; локальные записи можно сохранять без жанров."}
				</p>
				<small>
					В продукте каталог загружается с настроенного публичного MusicBrainz
					или собственного сервера. Офлайн-макет показывает небольшой набор
					вымышленных демонстрационных значений, не полный каталог MusicBrainz;
					сеть и обновления не выполняются.
				</small>
			</div>
			<div className={styles.genreLibraryControls}>
				<Button
					className={styles.genreLibraryButton}
					onPress={() => onLoadedChange(true)}
				>
					{buttonLabel}
				</Button>
				<span className="block text-[11px] text-[var(--muted)]">
					Автоматическое обновление: раз в неделю в продукте; в макете — только
					вручную.
				</span>
			</div>
		</div>
	);
}
