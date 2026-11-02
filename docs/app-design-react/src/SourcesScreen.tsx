import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "react-aria-components";
import { DirectoryPicker } from "./DirectoryPicker";
import type { SourceFiles } from "./catalogData";
import { rootLocations } from "./sourceInventory";

type Mode = "in_place" | "staged";
export interface SourceRoot {
	name: string;
	path: string;
	mode: Mode;
	available: boolean | null;
	checked: string;
}
export const initialSourceRoots: SourceRoot[] = [
	{
		name: "Входящие",
		path: "/mnt/inbox",
		mode: "staged",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		name: "Архивный диск",
		path: "/mnt/archive",
		mode: "in_place",
		available: false,
		checked: "25.09.2026 18:40",
	},
];

export function SourcesScreen({
	roots,
	onRoots,
	files,
	removed,
	onRemove,
	workDirectory = "",
}: {
	roots: SourceRoot[];
	onRoots: (roots: SourceRoot[]) => void;
	files: SourceFiles;
	removed: Set<string>;
	onRemove: (rootId: string) => void;
	workDirectory?: string;
}) {
	const [adding, setAdding] = useState(false);
	const [name, setName] = useState("");
	const [path, setPath] = useState("/mnt/music");
	const [mode, setMode] = useState<Mode | "">("");
	const [picker, setPicker] = useState(false);
	const [busy, setBusy] = useState(false);
	const [notice, setNotice] = useState("");
	const [removal, setRemoval] = useState<{
		id: string;
		path: string;
		locations: string[];
	} | null>(null);
	const process = (root: SourceRoot) =>
		setNotice(
			root.mode === "staged" && !workDirectory
				? "Демо: обработка через рабочую копию не начата — сначала задайте общую рабочую папку в Настройках. Источники и файлы не читались."
				: `Демо: предпосылка выбора рабочей папки для ${root.path} выполнена. Реальные файлы, доступность источника и права не проверялись; копирование и анализ не запускались.`,
		);
	return (
		<section>
			<header className="demo-object-header">
				<div>
					<h1>Источники</h1>
					<p>
						Серверные пути, политика источника — только чтение. Режим обработки
						выбирается отдельно; файлы не загружаются перетаскиванием.
					</p>
				</div>
				<div className="demo-object-actions">
					<Button
						onPress={() => {
							setAdding(true);
							setName("");
							setPath("/mnt/music");
							setMode("");
						}}
					>
						Добавить источник
					</Button>
					<Button
						onPress={() =>
							setNotice(
								"Демо: сканирование поставлено в очередь. Рабочая папка не требуется для перечисления файлов; копирование и анализ выполняются отдельно. Реальные каталоги не читались.",
							)
						}
					>
						Сканировать · демо
					</Button>
				</div>
			</header>
			{notice && <p role="status">{notice}</p>}
			{adding && (
				<section className="demo-panel">
					<h2>Новый источник</h2>
					<label className="demo-search">
						Имя источника
						<input
							value={name}
							onChange={(event) => setName(event.currentTarget.value)}
							placeholder="Например, Музыкальная библиотека"
						/>
					</label>
					<label className="demo-search">
						Каталог на сервере
						<input value={path} readOnly />
					</label>
					<Button onPress={() => setPicker(true)}>Выбрать папку</Button>
					<p>Только каталог; файлы не загружаются.</p>
					<fieldset>
						<legend>Режим обработки · выберите явно</legend>
						<label>
							<input
								type="radio"
								name="source-mode"
								value="in_place"
								checked={mode === "in_place"}
								onChange={() => setMode("in_place")}
							/>{" "}
							На месте
						</label>
						<p>
							Анализ выполняется по исходному файлу; сам источник остаётся
							только для чтения.
						</p>
						<label>
							<input
								type="radio"
								name="source-mode"
								value="staged"
								checked={mode === "staged"}
								onChange={() => setMode("staged")}
							/>{" "}
							Через рабочую копию
						</label>
						<p>
							Для обработки создаётся принадлежащая работе рабочая копия аудио в
							общей рабочей папке сервера. Папка требуется до обработки, но не
							для добавления источника.
						</p>
					</fieldset>
					<p>
						Политика источника: только чтение — независимо от выбранного режима.
					</p>
					<div className="demo-object-actions">
						<Button
							onPress={() => {
								if (!name.trim()) {
									setNotice("Укажите имя источника.");
									return;
								}
								if (!mode) {
									setNotice(
										"Явно выберите режим обработки: «На месте» или «Через рабочую копию».",
									);
									return;
								}
								onRoots([
									...roots,
									{
										name: name.trim(),
										path,
										mode,
										available: null,
										checked: "—",
									},
								]);
								setAdding(false);
								setNotice(
									`Демо: источник добавлен только в макет, режим «${mode === "staged" ? "Через рабочую копию" : "На месте"}». Серверный каталог не открывался и не сканировался; рабочая папка требуется только перед обработкой.`,
								);
							}}
						>
							Добавить источник
						</Button>
						<Button onPress={() => setAdding(false)}>Отмена</Button>
					</div>
				</section>
			)}
			<section className="demo-panel">
				<h2>Подключённые каталоги</h2>
				<div className="demo-preview-table">
					<table>
						<thead>
							<tr>
								{[
									"Имя",
									"Путь",
									"Режим обработки",
									"Политика источника",
									"Доступность",
									"Последняя проверка",
									"Действия",
								].map((label) => (
									<th key={label} scope="col">
										{label}
									</th>
								))}
							</tr>
						</thead>
						<tbody>
							{roots.map((root, index) => (
								<tr key={`${root.path}:${index}`}>
									<td>{root.name}</td>
									<td>
										<code>{root.path}</code>
									</td>
									<td>
										<label className="demo-search">
											Режим обработки: {root.name}
											<select
												value={root.mode}
												onChange={(event) => {
													const next = event.currentTarget.value as Mode;
													onRoots(
														roots.map((item, position) =>
															index === position
																? { ...item, mode: next }
																: item,
														),
													);
												}}
											>
												<option value="in_place">На месте</option>
												<option value="staged">Через рабочую копию</option>
											</select>
										</label>
										<p>Изменение влияет на будущую обработку.</p>
										{root.mode === "staged" && (
											<p>
												Общая рабочая папка: {workDirectory || "Не выбрана"} ·{" "}
												<a href="#settings">Настроить</a>
											</p>
										)}
									</td>
									<td>Только чтение</td>
									<td>
										{root.available === null
											? "Демо · не проверен"
											: root.available
												? "Доступен"
												: "Недоступен"}
									</td>
									<td>{root.checked}</td>
									<td>
										{root.available === null ? (
											<span>Требует проверки</span>
										) : root.available ? (
											<a href="#review">Разобрать группы</a>
										) : (
											<Button
												onPress={() =>
													setNotice(
														"Демо: /mnt/archive недоступен. Управляемые публикации сохранены.",
													)
												}
											>
												Проверить
											</Button>
										)}
										<Button onPress={() => process(root)}>
											Обработать · демо
										</Button>
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</section>
			<details>
				<summary>Демонстрация удаления источника</summary>
				<section className="demo-panel">
					<h2>Удаление каталога источника</h2>
					<p>
						Подтверждение показывает точный путь и число записей инвентаря.
						Файлы на диске и текущие публикации не затрагиваются. Включите
						активную операцию, чтобы увидеть отказ.
					</p>
					<label>
						<input
							type="checkbox"
							checked={busy}
							onChange={(event) => setBusy(event.currentTarget.checked)}
						/>
						Для /mnt/music выполняется операция
					</label>
					<div className="demo-preview-table">
						<table>
							<thead>
								<tr>
									{[
										"Идентификатор",
										"Путь на сервере",
										"Записей",
										"Сценарий",
										"Действие",
									].map((label) => (
										<th scope="col" key={label}>
											{label}
										</th>
									))}
								</tr>
							</thead>
							<tbody>
								{[
									[
										"root-inbox",
										"/mnt/inbox",
										"Общий файл остаётся связанным с резервным каталогом",
									],
									[
										"root-library",
										"/mnt/music",
										"Последний FLAC-исходник снимает связь; MP3 не выбирается автоматически",
									],
								].map(([id, path, scenario]) => (
									<tr key={id}>
										<td>{id}</td>
										<td>{path}</td>
										<td>{rootLocations(files, id).length}</td>
										<td>{scenario}</td>
										<td>
											{removed.has(id) ? (
												"Удалён в демо"
											) : (
												<Button
													onPress={() => {
														if (id === "root-library" && busy) {
															setNotice(
																"Удаление отклонено: для этого каталога источника выполняется операция.",
															);
															return;
														}
														setRemoval({
															id,
															path,
															locations: rootLocations(files, id)
																.map((location) => location.id)
																.sort(),
														});
													}}
												>
													Удалить каталог
												</Button>
											)}
										</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
					<p>
						После удаления последней записи связи позиций снимаются, кэш анализа
						по SHA-256 сохраняется. Выбранный файл очищается без
						автопереключения. Публикация и медиатека остаются.
					</p>
				</section>
			</details>
			<p>
				Недоступность исходного диска не удаляет существующие управляемые
				публикации.
			</p>
			<p>
				Общая рабочая папка рабочих копий: {workDirectory || "не выбрана"} ·{" "}
				<a href="#settings">Настроить в Настройках</a>
			</p>
			{picker && (
				<DirectoryPicker
					initialPath={path}
					onClose={() => setPicker(false)}
					onChoose={(value) => {
						setPath(value);
						setPicker(false);
					}}
				/>
			)}
			<Modal
				isOpen={Boolean(removal)}
				onOpenChange={(open) => {
					if (!open) setRemoval(null);
				}}
				isDismissable
			>
				<Dialog aria-labelledby="remove-root-title">
					<Heading slot="title" id="remove-root-title">
						Удалить каталог источника {removal?.path}?
					</Heading>
					<p>
						Путь: {removal?.path}
						<br />
						Записей о файлах в инвентаре: {removal?.locations.length}.
					</p>
					<p>
						Удаляются только запись каталога, связанные с ней записи о файлах и
						сведения об источнике. Физические исходники, объекты медиатеки и
						текущие публикации сохраняются.
					</p>
					<p>
						Связь позиции с источником сохраняется, если у файла осталась запись
						в другом каталоге. При удалении последней записи о файле связи с ним
						снимаются; кэш анализа по SHA-256 остаётся. Выбранный исходник
						очищается без автоматической замены.
					</p>
					<p>
						Перед подтверждением список файлов и наличие выполняемой операции
						будут перепроверены. Демо не обращается к реальной файловой системе.
					</p>
					<Button onPress={() => setRemoval(null)}>Отмена</Button>
					<Button
						onPress={() => {
							if (!removal) return;
							if (busy && removal.id === "root-library")
								setNotice(
									"Подтверждение устарело: началась операция; каталог не удалён.",
								);
							else if (
								rootLocations(files, removal.id)
									.map((location) => location.id)
									.sort()
									.join("|") !== removal.locations.join("|")
							)
								setNotice(
									"Подтверждение устарело: список файлов в инвентаре изменился. Пересмотрите данные.",
								);
							else {
								onRemove(removal.id);
								setNotice(
									`Демо: каталог ${removal.path} удалён из инвентаря. Файлы на диске, публикации и медиатека не изменились; другой исходник автоматически не выбран.`,
								);
							}
							setRemoval(null);
						}}
					>
						Подтвердить
					</Button>
				</Dialog>
			</Modal>
		</section>
	);
}
