import { useRef, useState } from "react";
import { Button } from "./ui";
import { demoGenres } from "./GenreLibrary";

export type MetadataField = { key: string; label: string; value: string };
type GenreField = { value: string[]; displayValue: string; loaded: boolean };

export function ObjectMetadataForm({
	label,
	fields,
	readOnlyFields = [],
	required = [],
	genreField,
	onSave,
}: {
	label: string;
	fields: MetadataField[];
	readOnlyFields?: MetadataField[];
	required?: string[];
	genreField?: GenreField;
	onSave: (values: Record<string, string>, genres?: string[]) => void;
}) {
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState<Record<string, string>>({});
	const [draftGenres, setDraftGenres] = useState<string[]>([]);
	const [genresChanged, setGenresChanged] = useState(false);
	const [genreSearch, setGenreSearch] = useState("");
	const [genrePickerOpen, setGenrePickerOpen] = useState(false);
	const [error, setError] = useState("");
	const actionRef = useRef<HTMLButtonElement>(null);
	const beginEdit = () => {
		setDraft(Object.fromEntries(fields.map(({ key, value }) => [key, value])));
		setDraftGenres(genreField?.value ?? []);
		setGenresChanged(false);
		setGenreSearch("");
		setGenrePickerOpen(false);
		setError("");
		setEditing(true);
	};
	const cancel = () => {
		setEditing(false);
		setError("");
		setDraft({});
		setDraftGenres([]);
		setGenresChanged(false);
		setGenrePickerOpen(false);
		requestAnimationFrame(() => actionRef.current?.focus());
	};
	const save = () => {
		const missing = fields.find(
			({ key }) => required.includes(key) && !draft[key]?.trim(),
		);
		if (missing) {
			setError(`Заполните поле «${missing.label}».`);
			return;
		}
		const values = Object.fromEntries(
			fields.map(({ key }) => [key, draft[key] ?? ""]),
		);
		if (genreField && genresChanged) onSave(values, draftGenres);
		else onSave(values);
		setEditing(false);
		setError("");
		setDraft({});
		setDraftGenres([]);
		setGenresChanged(false);
		setGenrePickerOpen(false);
	};
	return (
		<section
			aria-label={label}
			className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			onKeyDown={(event) => {
				if (editing && event.key === "Escape") {
					event.preventDefault();
					cancel();
				}
			}}
		>
			<div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
				<h2 className="m-0 text-sm font-semibold">{label}</h2>
				<div className="flex min-h-[30px] items-center gap-2">
					<Button
						ref={actionRef}
						className="w-[132px] shrink-0"
						onPress={editing ? save : beginEdit}
					>
						{editing ? "Сохранить" : "Редактировать"}
					</Button>
					<Button
						className={`w-[132px] shrink-0 ${editing ? "visible" : "invisible"}`}
						isDisabled={!editing}
						aria-hidden={!editing}
						onPress={cancel}
					>
						Отменить
					</Button>
				</div>
			</div>
			<div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
				{fields.map(({ key, label: fieldLabel, value }) => (
					<label
						key={key}
						className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
					>
						<span>{fieldLabel}</span>
						<input
							aria-label={fieldLabel}
							value={editing ? (draft[key] ?? "") : value}
							readOnly={!editing}
							className={`h-8 min-w-0 w-full truncate rounded-[var(--control-radius)] border border-transparent bg-transparent px-2 text-sm text-[var(--ink)] outline-none ${editing ? "border-b-[var(--control)] focus-visible:border-b-[var(--accent)] focus-visible:ring-2 focus-visible:ring-[var(--focus)]" : ""}`}
							onChange={(event) => {
								setDraft((current) => ({
									...current,
									[key]: event.target.value,
								}));
								setError("");
							}}
						/>
					</label>
				))}
				{genreField && (
					<div className="grid min-w-0 content-start gap-1 border-0 p-0 text-xs text-[var(--muted)]">
						<span>Жанры</span>
						<div className="relative">
							<button
								type="button"
								aria-label="Жанры"
								aria-expanded={editing && genrePickerOpen}
								disabled={editing && !genreField.loaded}
								onClick={() => {
									if (editing) setGenrePickerOpen((open) => !open);
								}}
								className={`h-8 w-full truncate rounded-[var(--control-radius)] px-2 text-left text-sm text-[var(--ink)] ${editing ? "border border-[var(--control)] focus-visible:ring-2 focus-visible:ring-[var(--focus)]" : ""}`}
							>
								{editing
									? draftGenres
											.map(
												(id) =>
													demoGenres.find((genre) => genre.id === id)?.name ??
													id,
											)
											.join(", ") || "Выбрать жанры…"
									: genreField.displayValue || "—"}
							</button>
							{editing && genrePickerOpen && genreField.loaded && (
								<div className="absolute z-20 mt-1 w-full min-w-[220px] rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-2 shadow-lg">
									<input
										aria-label="Поиск жанров"
										value={genreSearch}
										onChange={(event) => setGenreSearch(event.target.value)}
										placeholder="Поиск жанров…"
										className="mb-2 h-8 w-full rounded border border-[var(--line)] bg-transparent px-2 text-sm"
									/>
									<div className="max-h-48 overflow-y-auto">
										{demoGenres
											.filter((genre) =>
												genre.name
													.toLocaleLowerCase()
													.includes(genreSearch.trim().toLocaleLowerCase()),
											)
											.map((genre) => (
												<label
													key={genre.id}
													className="flex items-center gap-2 py-1 text-sm"
												>
													<input
														type="checkbox"
														aria-label={genre.name}
														checked={draftGenres.includes(genre.id)}
														onChange={(event) => {
															setGenresChanged(true);
															setDraftGenres((current) =>
																event.target.checked
																	? [...current, genre.id]
																	: current.filter((id) => id !== genre.id),
															);
														}}
													/>
													{genre.name}
												</label>
											))}
									</div>
								</div>
							)}
						</div>
					</div>
				)}
				{readOnlyFields.map(({ key, label: fieldLabel, value }) => (
					<label
						key={key}
						className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
					>
						<span>{fieldLabel}</span>
						<input
							aria-label={fieldLabel}
							value={value}
							readOnly
							className="h-8 min-w-0 w-full truncate rounded-[var(--control-radius)] border border-transparent bg-transparent px-2 text-sm text-[var(--muted)] outline-none"
						/>
					</label>
				))}
			</div>
			<p aria-live="polite" className="m-0 min-h-4 text-xs text-[var(--error)]">
				{error}
			</p>
		</section>
	);
}
