import { useState } from "react";
import {
	Button,
	Dialog,
	Heading,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Modal,
	Popover,
	Select,
	SelectValue,
	TextField,
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import { formStyles as styles } from "./formStyles";

export interface DemoSettings {
	toolsPath: string;
	workDirectory: string;
	toolsChecked: boolean;
	toolsUpdated: boolean;
	pathChecked: boolean;
	genresLoaded: boolean;
	outputCandidate: string;
	outputDemoExecution: "idle" | "queued" | "running" | "claimed";
}

export const initialDemoSettings: DemoSettings = {
	toolsPath: "/var/lib/melotrove/tools",
	workDirectory: "",
	toolsChecked: false,
	toolsUpdated: false,
	pathChecked: false,
	genresLoaded: false,
	outputCandidate: "",
	outputDemoExecution: "idle",
};

export type SettingsScreenProps = {
	settings: DemoSettings;
	onSettings: (settings: DemoSettings) => void;
};

type PickerTarget = "tools" | "work" | "output";
const executionLabels = {
	idle: "Нет активных операций",
	queued: "Есть только ожидающие операции",
	running: "Выполняется операция",
	claimed: "Операция уже захвачена обработчиком",
} as const;

export function SettingsScreen({ settings, onSettings }: SettingsScreenProps) {
	const [picker, setPicker] = useState<PickerTarget | null>(null);
	const [notice, setNotice] = useState("");
	const [confirming, setConfirming] = useState<string | null>(null);
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	const blocked = ["running", "claimed"].includes(settings.outputDemoExecution);
	const samePath =
		!settings.outputCandidate || settings.outputCandidate === "/srv/music";
	const candidate = settings.outputCandidate;

	const requestOutputReset = () => {
		if (!candidate || candidate === "/srv/music") {
			setNotice("Выберите путь, отличный от текущего каталога публикации.");
			return;
		}
		if (blocked) {
			setNotice(
				"Смена каталога публикации заблокирована: выполняется или захвачена операция.",
			);
			return;
		}
		setConfirming(candidate);
	};
	const confirmOutputReset = () => {
		if (["running", "claimed"].includes(settings.outputDemoExecution)) {
			setConfirming(null);
			setNotice(
				"Подтверждение устарело: состояние изменилось на выполняющуюся или захваченную операцию. Смена не выполнена.",
			);
			return;
		}
		if (settings.outputCandidate !== confirming) {
			setConfirming(null);
			setNotice(
				"Подтверждение устарело: демонстрационные параметры изменились. Смена не выполнена.",
			);
			return;
		}
		setConfirming(null);
		setNotice(
			"В макете текущий каталог публикации, очереди и демонстрационные публикации не изменены; реального сброса нет.",
		);
	};

	return (
		<section className={styles.page} aria-labelledby="settings-title">
			<header
				className={`${styles.pageHeader} flex flex-wrap items-center justify-between gap-3`}
			>
				<div>
					<h1 className={styles.pageTitle} id="settings-title">
						Настройки
					</h1>
					<p className={styles.pageDescription}>
						Тема доступна в верхней панели. Рабочие настройки продукта
						сохраняются в БД; здесь демонстрация.
					</p>
				</div>
				<Button
					onPress={() => {
						update({ toolsChecked: true });
						setNotice(
							"Демо: есть обновление FFmpeg/ffprobe. Оно не установлено автоматически.",
						);
					}}
				>
					Проверить обновления
				</Button>
			</header>
			{notice && (
				<p className={styles.formNotice} role="status" aria-live="polite">
					{notice}
				</p>
			)}
			<section className={`${styles.panel} grid gap-2`}>
				<h2 className="mb-2 text-sm font-semibold">Библиотека жанров</h2>
				<GenreLibrary
					loaded={settings.genresLoaded}
					onLoadedChange={(genresLoaded) => update({ genresLoaded })}
					buttonLabel="Загрузить / обновить каталог в демо"
				/>
			</section>
			<div className={styles.settingsGrid}>
				<section className={styles.panel}>
					<h2>Каталог публикации</h2>
					<TextField className={styles.formField} isReadOnly value="/srv/music">
						<Label>Текущий каталог публикации · только чтение</Label>
						<Input />
					</TextField>
					<div className={styles.formField}>
						<div className={styles.objectActions}>
							<TextField
								id="settings-output-candidate"
								value={settings.outputCandidate}
								isReadOnly
								aria-describedby="settings-output-candidate-help"
							>
								<Label>Новый путь · кандидат, ещё не применён</Label>
								<Input placeholder="Выберите папку на сервере" />
							</TextField>
							<Button onPress={() => setPicker("output")}>Выбрать папку</Button>
						</div>
						<small id="settings-output-candidate-help">
							Выбор папки только задаёт кандидата. Отмена в окне выбора ничего
							не меняет.
						</small>
					</div>
					<div className={styles.formField}>
						<Select
							selectedKey={settings.outputDemoExecution}
							onSelectionChange={(key) =>
								key &&
								update({
									outputDemoExecution:
										key as DemoSettings["outputDemoExecution"],
								})
							}
						>
							<Label>Состояние выполнения · только для демонстрации</Label>
							<Button>
								<SelectValue />
							</Button>
							<Popover>
								<ListBox>
									{Object.entries(executionLabels).map(([key, label]) => (
										<ListBoxItem id={key} key={key}>
											{key === "claimed"
												? "Операция захвачена обработчиком"
												: label}
										</ListBoxItem>
									))}
								</ListBox>
							</Popover>
						</Select>
						<small>
							Это демонстрационный переключатель состояния, не настройка
							продукта. Сейчас: {executionLabels[settings.outputDemoExecution]}.
						</small>
					</div>
					<p role={blocked ? "alert" : "note"}>
						{blocked
							? `Смена заблокирована: ${executionLabels[settings.outputDemoExecution].toLocaleLowerCase()}.`
							: settings.outputDemoExecution === "queued"
								? "Ожидающие задачи не блокируют смену; при подтверждении все очереди и задания будут сброшены."
								: "Активных операций нет в выбранном демонстрационном состоянии."}
					</p>
					<div className="mt-4 rounded-md border border-[var(--line)] bg-[var(--surface)] p-4 [&_h2]:m-0 [&_h2]:text-base [&_p]:text-[var(--muted)] [&_p]:text-[13px] [&_label]:grid [&_label]:max-w-[420px] [&_label]:gap-[6px] [&_select]:min-h-10 [&_select]:px-[10px] [&_select]:py-[6px]">
						<strong>Что произойдёт при подтверждении смены</strong>
						<ul>
							<li>
								Сменится каталог публикации: <code>/srv/music</code> →{" "}
								<code>{candidate || "—"}</code>.
							</li>
							<li>
								Все очереди и задания будут сброшены; ожидающие задачи смене не
								препятствуют.
							</li>
							<li>
								Все ссылки БД на прежний каталог публикации, включая строки
								публикаций и ссылки на размещённые в нём служебные файлы, будут
								удалены.
							</li>
							<li>Физические файлы в старом каталоге не удаляются.</li>
							<li>Автоматической повторной публикации не будет.</li>
							<li>
								Инвентарь источников, сущности библиотеки, связи с источниками и
								анализы сохраняются.
							</li>
							<li>
								Общая рабочая папка рабочих копий анализа источников и её
								рабочие копии аудио сохраняются; их учёт в БД (принадлежность,
								соответствие источнику и пригодность) и незавершённые
								запрошенные шаги анализа не сбрасываются.
							</li>
							<li>
								Сброшенная обработка не возобновляется автоматически ни
								вследствие сброса, ни после последующего перезапуска; явный
								запуск может переиспользовать пригодную копию.
							</li>
						</ul>
					</div>
					<div className={styles.objectActions}>
						<Button
							isDisabled={blocked || samePath}
							onPress={requestOutputReset}
						>
							Подтвердить смену каталога публикации
						</Button>
					</div>
					{samePath && candidate && (
						<p>
							Кандидат совпадает с текущим каталогом публикации: менять нечего,
							сброс не запускается.
						</p>
					)}
					<p>
						Статический макет: реального сброса нет. Текущий путь, очереди, БД,
						файлы и демонстрационные публикации не изменяются.
					</p>
				</section>
				<section className={styles.panel}>
					<h2>Рабочая папка анализа источников</h2>
					<TextField
						className={styles.formField}
						value={settings.workDirectory}
						isReadOnly
					>
						<Label>Общая папка для рабочих копий аудио</Label>
						<div className={styles.objectActions}>
							<Input aria-describedby="settings-source-work-help" />
							<Button onPress={() => setPicker("work")}>Выбрать папку</Button>
						</div>
						<small id="settings-source-work-help">
							Серверный путь, общий для всех источников с рабочими копиями;
							отдельный от каталога публикации.{" "}
							{settings.workDirectory
								? `Текущий выбор: ${settings.workDirectory}.`
								: "Не задана."}
						</small>
					</TextField>
					<p>
						Настройте её до начала обработки через рабочие копии. Первый запуск
						и добавление источника не требуют выбора. В реальном продукте смена
						запрещена, пока папку используют операции или в ней остаются
						сохранённые копии; автоматического переноса нет.
					</p>
					<p>
						Статический макет не проверяет файловую систему и не моделирует
						занятые операции или сохранённые копии; путь не проверен.
					</p>
				</section>
				<section className={styles.panel}>
					<h2>Инструменты · Linux amd64</h2>
					<div className={styles.previewTable}>
						<table>
							<thead>
								<tr>
									<th>Инструмент</th>
									<th>Активная версия</th>
									<th>Источник</th>
									<th>Проверено</th>
								</tr>
							</thead>
							<tbody>
								<tr>
									<td>ffmpeg</td>
									<td>{settings.toolsUpdated ? "8.0.1" : "8.0"}</td>
									<td>BtbN/FFmpeg-Builds</td>
									<td>SHA-256 если опубликован + -version</td>
								</tr>
								<tr>
									<td>ffprobe</td>
									<td>{settings.toolsUpdated ? "8.0.1" : "8.0"}</td>
									<td>BtbN/FFmpeg-Builds</td>
									<td>SHA-256 если опубликован + -version</td>
								</tr>
								<tr>
									<td>fpcalc</td>
									<td>1.5.1</td>
									<td>AcoustID Chromaprint</td>
									<td>-version</td>
								</tr>
							</tbody>
						</table>
					</div>
					<div className="min-w-0 rounded-md border border-[var(--line)] bg-[var(--raised)] p-3">
						<TextField
							className={styles.formField}
							value={settings.toolsPath}
							isReadOnly
						>
							<Label>Постоянный каталог инструментов</Label>
							<div className={styles.objectActions}>
								<Input aria-describedby="settings-tools-path-help" />
								<Button onPress={() => setPicker("tools")}>
									Выбрать папку
								</Button>
							</div>
							<small id="settings-tools-path-help">
								Каталог на сервере для управляемых инструментов.
							</small>
						</TextField>
						<p>
							Версии условные. Проверка только уведомляет; установка и откат
							явные.
						</p>
						<div className={styles.objectActions}>
							{settings.toolsChecked && !settings.toolsUpdated && (
								<Button onPress={() => setConfirming("tools-install")}>
									Установить обновление
								</Button>
							)}
							{settings.toolsUpdated && (
								<Button onPress={() => setConfirming("tools-rollback")}>
									Откатить к 8.0
								</Button>
							)}
						</div>
					</div>
				</section>
				<section className={styles.panel}>
					<h2 className="mb-2 text-sm font-semibold">Обработка и публикация</h2>
					<dl className="my-[10px] grid grid-cols-[minmax(100px,1fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] [&_dd]:[overflow-wrap:anywhere] [&_dd]:my-0 [&_dt]:text-[var(--muted)]">
						{[
							[
								"Полный уверенный релиз",
								"Автоматически сопоставить и опубликовать",
							],
							[
								"Частичный / неоднозначный",
								"Ручной разбор; частичная публикация разрешена",
							],
							["Исходники", "Сохранить; неразмеченные остаются во входящих"],
							[
								"Оценка",
								"Политика matching-rapidfuzz-v1; применимость ≥0.70. Макет не вычисляет уверенность",
							],
							["Формат / каталог", "MKA · /srv/music"],
							["Метаданные / тексты", "MusicBrainz / LRCLIB"],
						].map(([term, value]) => (
							<div key={term}>
								<dt>{term}</dt>
								<dd>{value}</dd>
							</div>
						))}
					</dl>
				</section>
			</div>
			{picker && (
				<DirectoryPicker
					key={picker}
					initialPath={
						picker === "tools"
							? settings.toolsPath
							: picker === "work"
								? settings.workDirectory || "/"
								: settings.outputCandidate || "/srv/music"
					}
					onClose={() => setPicker(null)}
					onChoose={(path) => {
						if (picker === "tools") {
							update({ toolsPath: path, pathChecked: false });
							setNotice(
								"Демо: выбран серверный каталог. Проверка прав и конфигурации не выполнялась.",
							);
						} else if (picker === "work") {
							update({ workDirectory: path });
							setNotice(
								"Демо: общая рабочая папка выбрана и сохранена только в памяти макета. Использование и файлы не проверялись.",
							);
						} else {
							update({ outputCandidate: path });
							setNotice(
								`Демо: выбран новый каталог публикации ${path}. Текущий путь не изменён; серверный каталог не проверялся.`,
							);
						}
						setPicker(null);
					}}
				/>
			)}
			{confirming && confirming.startsWith("tools-") && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setConfirming(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">
							{confirming === "tools-install"
								? "Установить обновление инструментов?"
								: "Откатить инструменты к 8.0?"}
						</Heading>
						<p>
							{confirming === "tools-install" ? (
								<>
									Скачать из доверенного HTTPS-источника; проверить
									опубликованный SHA-256, если он доступен; запустить{" "}
									<code>-version</code> у каждого исполняемого файла. Подписи и
									дополнительные политики здесь не предполагаются. Действие
									демонстрационное.
								</>
							) : (
								"Будет выбрана предыдущая проверенная версия. Действие демонстрационное."
							)}
						</p>
						<Button
							onPress={() => {
								const install = confirming === "tools-install";
								update({ toolsUpdated: install });
								setConfirming(null);
							}}
						>
							Подтвердить
						</Button>
						<Button onPress={() => setConfirming(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
			{confirming && !confirming.startsWith("tools-") && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setConfirming(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">
							Подтвердить смену каталога публикации?
						</Heading>
						<p>
							<strong>/srv/music → {confirming}</strong>
						</p>
						<p>
							Все очереди и задания будут сброшены. Ожидающие задачи не
							блокируют смену.
						</p>
						<p>
							Все ссылки БД на прежний каталог публикации, включая строки
							публикаций и ссылки на размещённые в нём служебные файлы, будут
							удалены. Физические файлы в старом каталоге не удаляются.
							Автоматической повторной публикации не будет.
						</p>
						<p>
							Инвентарь источников, сущности библиотеки, связи с источниками и
							анализы сохраняются.
						</p>
						<p>
							Общая рабочая папка рабочих копий анализа источников и её рабочие
							копии аудио сохраняются вместе с учётом в БД (принадлежность,
							соответствие источнику и пригодность) и незавершёнными
							запрошенными шагами анализа. Сброшенная обработка не
							возобновляется автоматически ни вследствие сброса, ни после
							последующего перезапуска; явный запуск может переиспользовать
							пригодную копию.
						</p>
						<p>
							Подтверждение только демонстрирует взаимодействие. Текущий каталог
							публикации, очереди, БД, файлы и демонстрационные публикации
							останутся без изменений.
						</p>
						<Button onPress={confirmOutputReset}>Подтвердить</Button>
						<Button onPress={() => setConfirming(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}
