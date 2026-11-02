import { useState } from "react";
import {
	originalFiles,
	type SourceFile,
	type SourceFiles,
} from "./catalogData";

const duration = (seconds: number) => {
	if (!Number.isFinite(seconds) || seconds < 0) return "—";
	const total = Math.round(seconds);
	if (!Number.isSafeInteger(total)) return "—";
	return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
};

export function ComponentsScreen({
	files = originalFiles,
	fileId = "north-f3",
	selectedFile: controlledSelectedFile,
	onSelectFile,
}: {
	files?: SourceFiles;
	fileId?: string;
	selectedFile?: string | null;
	onSelectFile?: (id: string) => void;
}) {
	const [localSelectedFile, setLocalSelectedFile] = useState<string | null>(
		null,
	);
	const selectedFile =
		controlledSelectedFile === undefined
			? localSelectedFile
			: controlledSelectedFile;
	const file = files[fileId] ?? Object.values(files)[0];
	const selectFile = (id: string) => {
		setLocalSelectedFile(id);
		onSelectFile?.(id);
	};
	if (!file)
		return (
			<section>
				<h1>Компоненты · плотный интерфейс</h1>
				<p>Нет файла для назначения.</p>
			</section>
		);
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>Компоненты · плотный интерфейс</h1>
					<p>
						Обе темы используют одинаковую геометрию и семантические состояния.
					</p>
				</div>
			</header>
			<section className="demo-panel">
				<h2>Состояния и действия</h2>
				<div className="demo-object-actions">
					<span className="status success">Высокая уверенность</span>
					<span className="status warning">Есть отличия</span>
					<span className="status error">Ошибка</span>
					<span className="status">Без связи с провайдером</span>
					<button type="button" className="primary">
						Основное действие
					</button>
					<button type="button">Вторичное действие</button>
					<button type="button" disabled>
						Нет назначения
					</button>
				</div>
			</section>
			<section className="demo-panel">
				<h2>Файл для назначения</h2>
				<div className="demo-object-actions">
					<FileChip
						file={file}
						selected={selectedFile === file.id}
						onSelect={selectFile}
					/>
				</div>
			</section>
			<section className="demo-panel">
				<h2>Техническая таблица</h2>
				<div className="demo-preview-table">
					<table>
						<thead>
							<tr>
								{["Поле", "Исходник", "Провайдер", "Различие"].map((label) => (
									<th scope="col" key={label}>
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							<tr>
								<th scope="row">TITLE</th>
								<td>Northbound (live?)</td>
								<td>Northbound</td>
								<td>
									<span className="status warning">
										Версия записи не подтверждена
									</span>
								</td>
							</tr>
							<tr>
								<th scope="row">DURATION</th>
								<td>3:58</td>
								<td>3:46</td>
								<td>+12 с</td>
							</tr>
						</tbody>
					</table>
				</div>
			</section>
			<p className="demo-migration-note">
				Политика matching-rapidfuzz-v1 утверждена; этот макет не рассчитывает
				уверенность или её вклады. Демонстрационный вердикт подписан явно. Цвет
				не заменяет текст.
			</p>
		</section>
	);
}

function FileChip({
	file,
	selected,
	onSelect,
}: {
	file: SourceFile;
	selected: boolean;
	onSelect: (id: string) => void;
}) {
	return (
		<button
			type="button"
			className={`file-chip ${selected ? "selected" : ""}`}
			draggable="true"
			aria-pressed={selected}
			title="Выбрать или перетащить файл"
			onClick={() => onSelect(file.id)}
			onDragStart={(event) => event.dataTransfer.setData("text/plain", file.id)}
		>
			<span className="grip" aria-hidden="true">
				⠿
			</span>
			<span>
				<strong>{file.name}</strong>
				<span className="sub">
					{file.codec} · {duration(file.duration)} ·{" "}
					{file.codec === "FLAC" ? "24 бит / 48 кГц" : "320 кбит/с"} ·{" "}
					{file.size}
				</span>
			</span>
		</button>
	);
}
