import { useEffect, useState } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Disclosure,
	DisclosureGroup,
	DisclosurePanel,
	Heading,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Modal,
	Popover,
	Radio,
	RadioGroup,
	Select,
	SelectValue,
	TextField,
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import type { SourceFiles } from "./catalogData";
import {
	completeSourceStep,
	createSourceScan,
	eligibleSourceCopies,
	failSourceCopyCleanup,
	initialSourceProcessingState,
	processSourceQueue,
	removeEligibleCopy,
	removeRootProcessingState,
	resetSourceProcessingQueue,
	retrySourceStep,
	type SourceProcessingState,
} from "./sourceProcessingModel";
import { rootLocations } from "./sourceInventory";
import { formStyles as styles } from "./formStyles";

type Mode = "in_place" | "staged";
export interface SourceRoot {
	id: string;
	name: string;
	path: string;
	mode: Mode;
	available: boolean | null;
	checked: string;
}
export const initialSourceRoots: SourceRoot[] = [
	{
		id: "root-inbox",
		name: "Входящие",
		path: "/mnt/inbox",
		mode: "staged",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-library",
		name: "Музыкальная библиотека",
		path: "/mnt/music",
		mode: "in_place",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-backup",
		name: "Резервный источник",
		path: "/mnt/backup",
		mode: "in_place",
		available: true,
		checked: "26.09.2026 10:20",
	},
	{
		id: "root-archive",
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
	initialProcessingState = initialSourceProcessingState,
	onProcessingState,
}: {
	roots: SourceRoot[];
	onRoots: (roots: SourceRoot[]) => void;
	files: SourceFiles;
	removed: Set<string>;
	onRemove: (rootId: string) => void;
	workDirectory?: string;
	initialProcessingState?: SourceProcessingState;
	onProcessingState?: (state: SourceProcessingState) => void;
}) {
	const [adding, setAdding] = useState(false);
	const [name, setName] = useState("");
	const [path, setPath] = useState("/mnt/music");
	const [mode, setMode] = useState<Mode | "">("");
	const [picker, setPicker] = useState(false);
	const [busy, setBusy] = useState(false);
	const [notice, setNotice] = useState("");
	const [processing, setProcessing] = useState(initialProcessingState);
	const [removal, setRemoval] = useState<{
		id: string;
		path: string;
		locations: string[];
	} | null>(null);
	useEffect(
		() => onProcessingState?.(processing),
		[onProcessingState, processing],
	);
	const updateProcessing = (next: SourceProcessingState) => setProcessing(next);
	return (
		<section className={styles.page}>
			<header
				className={`${styles.pageHeader} flex flex-wrap items-center justify-between gap-3`}
			>
				<div>
					<h1 className={styles.pageTitle}>Источники</h1>
					<p className={styles.pageDescription}>
						Серверные пути, политика источника — только чтение. Режим обработки
						выбирается отдельно; файлы не загружаются перетаскиванием.
					</p>
				</div>
				<div className={styles.objectActions}>
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
				</div>
			</header>
			{notice && (
				<p className={styles.formNotice} role="status">
					{notice}
				</p>
			)}
			{adding && (
				<section className={styles.panel}>
					<h2>Новый источник</h2>
					<TextField
						className={`${styles.searchField} max-w-none`}
						value={name}
						onChange={setName}
					>
						<Label>Имя источника</Label>
						<Input placeholder="Например, Музыкальная библиотека" />
					</TextField>
					<TextField
						className={`${styles.searchField} max-w-none`}
						value={path}
						isReadOnly
					>
						<Label>Каталог на сервере</Label>
						<Input />
					</TextField>
					<Button onPress={() => setPicker(true)}>Выбрать папку</Button>
					<p>Только каталог; файлы не загружаются.</p>
					<RadioGroup
						className="my-3 grid gap-1"
						aria-label="Режим обработки · выберите явно"
						value={mode}
						onChange={(value) => setMode(value as Mode)}
					>
						<Radio className={styles.modeChoice} value="in_place">
							На месте
						</Radio>
						<p className="my-[3px] ml-[25px] text-xs text-[var(--muted)]">
							Анализ выполняется по исходному файлу; сам источник остаётся
							только для чтения.
						</p>
						<Radio className={styles.modeChoice} value="staged">
							Через рабочую копию
						</Radio>
						<p className="my-[3px] ml-[25px] text-xs text-[var(--muted)]">
							Для обработки создаётся принадлежащая работе рабочая копия аудио в
							общей рабочей папке сервера. Папка требуется до обработки, но не
							для добавления источника.
						</p>
					</RadioGroup>
					<p>
						Политика источника: только чтение — независимо от выбранного режима.
					</p>
					<div className={styles.objectActions}>
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
										id: `root-${crypto.randomUUID()}`,
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
			<section className={styles.panel}>
				<h2>Подключённые каталоги</h2>
				<div className={styles.tableWrap}>
					<table className={styles.table}>
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
										<div className="grid max-w-none gap-[5px] text-xs text-[var(--muted)]">
											<Label>Режим обработки: {root.name}</Label>
											<Select
												selectedKey={root.mode}
												aria-label={`Режим обработки: ${root.name}`}
												onSelectionChange={(key) => {
													const next = String(key) as Mode;
													onRoots(
														roots.map((item, position) =>
															index === position
																? { ...item, mode: next }
																: item,
														),
													);
												}}
											>
												<Button>
													<SelectValue />
												</Button>
												<Popover>
													<ListBox>
														<ListBoxItem id="in_place">На месте</ListBoxItem>
														<ListBoxItem id="staged">
															Через рабочую копию
														</ListBoxItem>
													</ListBox>
												</Popover>
											</Select>
										</div>
										<p className="my-2">
											Изменение влияет на будущую обработку.
										</p>
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
											<Button
												onPress={() => {
													onRoots(
														roots.map((item) =>
															item.id === root.id
																? {
																		...item,
																		available: true,
																		checked: "Только что · демо",
																	}
																: item,
														),
													);
													setNotice(
														"Демо: источник отмечен доступным. Реальная файловая система не проверялась.",
													);
												}}
											>
												Проверить доступность · демо
											</Button>
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
										<Button
											isDisabled={root.available !== true}
											onPress={() => {
												updateProcessing(
													createSourceScan(
														processing,
														root.id,
														root.mode,
														files,
													),
												);
												setNotice(
													`Демо-сканирование ${root.path}: в очередь передано ${rootLocations(files, root.id).length} записей инвентаря. Файловая система не читалась; рабочая папка не нужна для scan. Обработка не запускалась.`,
												);
											}}
										>
											Сканировать · демо
										</Button>
										<Button
											isDisabled={root.available !== true}
											onPress={() => {
												if (root.mode === "staged" && !workDirectory) {
													setNotice(
														`Обработка ${root.path} не начата: задайте общую рабочую папку в Настройках. Сам scan не требует её.`,
													);
													return;
												}
												updateProcessing(
													processSourceQueue(
														createSourceScan(
															processing,
															root.id,
															root.mode,
															files,
														),
														root.id,
													),
												);
												setNotice(
													`Демо: явно запущена обработка очереди ${root.path}; проверка аудио выполнена, fingerprint завершился демонстрационной ошибкой. Файловая система не читалась.`,
												);
											}}
										>
											Обработать найденные · демо
										</Button>
									</td>
								</tr>
							))}
						</tbody>
					</table>
				</div>
			</section>
			<section
				className={styles.panel}
				aria-labelledby="source-processing-title"
			>
				<h2 id="source-processing-title">Обработка файлов · демо</h2>
				<p>
					Сканирование только ставит записи в очередь. Обработка, создание
					рабочей копии и проверка аудио запускаются отдельно; fingerprint в
					демонстрации завершается ошибкой. Повтор запускается явно.
				</p>
				<p className="text-xs text-[var(--muted)]">
					В продукте повторное сканирование сравнивает size и mtime. В исходных
					fixtures нет реального stat: макет не подтверждает свежесть файла на
					сервере.
				</p>
				<Button
					onPress={() =>
						updateProcessing(resetSourceProcessingQueue(processing))
					}
				>
					Сбросить очередь · демо
				</Button>
				<Button
					onPress={() => {
						if (
							!workDirectory &&
							processing.files.some(
								(file) => file.mode === "staged" && file.status !== "completed",
							)
						) {
							setNotice(
								"Обработка не начата: задайте рабочую папку в Настройках. Очередь и результаты сохранены.",
							);
							return;
						}
						updateProcessing(processSourceQueue(processing));
					}}
				>
					Запустить / перезапустить обработку · демо
				</Button>
				{processing.files.length === 0 ? (
					<p>Нет файлов, переданных в обработку.</p>
				) : (
					<ul className="m-0 grid gap-3 pl-5">
						{processing.files.map((file) => (
							<li key={file.id}>
								<strong>{file.path}</strong> · {file.status}
								<ul className="grid gap-2 pl-5">
									{file.steps.map((step) => (
										<li key={step.id}>
											{step.id === "audio_inspection"
												? "Проверка аудио"
												: "Fingerprint"}
											: {step.status}
											{step.error && <span role="alert"> · {step.error}</span>}
											{step.status === "failed" && (
												<Button
													onPress={() =>
														updateProcessing(
															retrySourceStep(processing, file.id, step.id),
														)
													}
												>
													Повторить шаг
												</Button>
											)}
											{step.status === "queued" && (
												<Button
													onPress={() =>
														updateProcessing(
															completeSourceStep(processing, file.id, step.id),
														)
													}
													isDisabled={file.mode === "staged" && !file.copyId}
												>
													Завершить шаг · демо
												</Button>
											)}
										</li>
									))}
								</ul>
							</li>
						))}
					</ul>
				)}
				<h3>Сохранённые рабочие копии</h3>
				<p>
					Принадлежат приложению; физические исходники и публикации ими не
					затрагиваются.
				</p>
				{processing.copies.length === 0 ? (
					<p>Нет сохранённых копий.</p>
				) : (
					<ul className="m-0 grid gap-2 pl-5">
						{processing.copies.map((copy) => {
							const eligible = eligibleSourceCopies(processing).some(
								(item) => item.id === copy.id,
							);
							return (
								<li key={copy.id}>
									{copy.fileId} · {copy.status} ·{" "}
									{eligible
										? "Можно очистить"
										: "Защищена: обработка не завершена"}
									{copy.cleanupError && (
										<span role="alert">
											{" "}
											· Ошибка очистки: {copy.cleanupError}
										</span>
									)}
									{eligible && (
										<>
											<Button
												onPress={() =>
													updateProcessing(
														failSourceCopyCleanup(processing, copy.id),
													)
												}
											>
												Смоделировать ошибку очистки
											</Button>
											<Button
												onPress={() =>
													updateProcessing(
														removeEligibleCopy(processing, copy.id),
													)
												}
											>
												Очистить копию · демо
											</Button>
										</>
									)}
								</li>
							);
						})}
					</ul>
				)}
			</section>
			<DisclosureGroup className="mt-2">
				<Disclosure>
					<Button slot="trigger">Демонстрация удаления источника</Button>
					<DisclosurePanel>
						<section className={styles.panel}>
							<h2>Удаление каталога источника</h2>
							<p>
								Подтверждение показывает точный путь и число записей инвентаря.
								Файлы на диске и текущие публикации не затрагиваются. Включите
								активную операцию, чтобы увидеть отказ.
							</p>
							<Checkbox isSelected={busy} onChange={setBusy}>
								Для /mnt/music выполняется операция
							</Checkbox>
							<div className={styles.tableWrap}>
								<table className={styles.table}>
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
										{roots.map((root) => (
											<tr key={root.id}>
												<td>{root.id}</td>
												<td>{root.path}</td>
												<td>{rootLocations(files, root.id).length}</td>
												<td>
													{root.id === "root-library"
														? "Последний FLAC-исходник снимает связь; MP3 не выбирается автоматически"
														: "Удаляется только inventory root; физический источник и публикации сохраняются"}
												</td>
												<td>
													{removed.has(root.id) ? (
														"Удалён в демо"
													) : (
														<Button
															onPress={() => {
																if (
																	busy ||
																	processing.files.some(
																		(file) =>
																			file.rootId === root.id &&
																			["queued", "running"].includes(
																				file.status,
																			),
																	)
																) {
																	setNotice(
																		"Удаление отклонено: для этого каталога источника выполняется операция.",
																	);
																	return;
																}
																setRemoval({
																	id: root.id,
																	path: root.path,
																	locations: rootLocations(files, root.id)
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
								После удаления последней записи связи позиций снимаются, кэш
								анализа по SHA-256 сохраняется. Выбранный файл очищается без
								автопереключения. Публикация и медиатека остаются.
							</p>
						</section>
					</DisclosurePanel>
				</Disclosure>
			</DisclosureGroup>
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
							if (
								busy ||
								processing.files.some(
									(file) =>
										file.rootId === removal.id &&
										["queued", "running"].includes(file.status),
								)
							) {
								setNotice(
									"Подтверждение устарело: для каталога выполняется операция; каталог не удалён. Пересмотрите состояние и подтвердите удаление повторно.",
								);
								return;
							} else if (
								rootLocations(files, removal.id)
									.map((location) => location.id)
									.sort()
									.join("|") !== removal.locations.join("|")
							)
								setNotice(
									"Подтверждение устарело: список файлов в инвентаре изменился. Пересмотрите данные.",
								);
							else {
								updateProcessing(
									removeRootProcessingState(processing, removal.id),
								);
								onRemove(removal.id);
								setNotice(
									`Демо: каталог ${removal.path} удалён из инвентаря. Принадлежащие приложению рабочие копии источника очищены; физические исходники, публикации и медиатека не изменились; другой исходник автоматически не выбран.`,
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
