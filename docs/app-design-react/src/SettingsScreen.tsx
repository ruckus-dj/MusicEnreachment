import { useEffect, useState } from "react";
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
import {
	activateDemoInstallation,
	availableToolReleases,
	canDeleteDemoInstallation,
	createDemoInstallation,
	initialDemoInstallations,
	isAbsoluteDemoServerPath,
	movePreflightDemo,
	type DemoInstallation,
	type ManagedToolPackage,
} from "./ToolLifecycleDemo";
import { setupToolExecutables } from "./setupToolsLifecycle";

export interface DemoSettings {
	toolsPath: string;
	workDirectory: string;
	toolsChecked: boolean;
	toolsUpdated: boolean;
	pathChecked: boolean;
	genresLoaded: boolean;
	outputCandidate: string;
	outputDemoExecution: "idle" | "queued" | "running" | "claimed";
	/** Optional demo state keeps older fixture constructions source-compatible. */
	currentOutput?: string;
	publicationFormat?: "" | "original" | "mka";
	musicBrainzMode?: "" | "public" | "self-hosted";
	musicBrainzURL?: string;
	musicBrainzThrottle?: boolean;
	musicBrainzDelay?: string;
	musicBrainzVerified?: boolean;
	lrclibEnabled?: boolean;
	sha256Enabled?: boolean;
	concurrency?: string;
	logLevel?: "" | "debug" | "info" | "warn" | "error";
	platformMismatch?: boolean;
	configurationProblem?: boolean;
	installations?: DemoInstallation[];
	selectedRelease?: Partial<Record<ManagedToolPackage, string>>;
	setupVerified?: Partial<Record<ManagedToolPackage, string>>;
	toolCatalog?: Record<ManagedToolPackage, string[]>;
	runtimeSaved?: boolean;
	sourceCleanupEnabled?: boolean;
	setupFailures?: Partial<Record<ManagedToolPackage, string>>;
	outputChecked?: boolean;
	simulateOutputNotEmpty?: boolean;
	simulateToolFailure?: boolean;
	simulateConnectionFailure?: boolean;
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
	currentOutput: "/srv/music",
	publicationFormat: "mka",
	musicBrainzMode: "",
	musicBrainzURL: "",
	musicBrainzThrottle: false,
	musicBrainzDelay: "",
	musicBrainzVerified: false,
	lrclibEnabled: true,
	sha256Enabled: true,
	concurrency: "4",
	logLevel: "info",
	platformMismatch: false,
	configurationProblem: false,
	installations: initialDemoInstallations,
	selectedRelease: {},
	toolCatalog: availableToolReleases,
	runtimeSaved: false,
	sourceCleanupEnabled: false,
};

export type SettingsScreenProps = {
	settings: DemoSettings;
	onSettings: (settings: DemoSettings) => void;
	/** Parent demo applies the shared library/output reset after successful confirmation. */
	onOutputReset?: (nextPath: string) => void;
	/** True while source work uses the shared path or any retained copy exists. */
	workDirectoryBusy?: boolean;
};

type PickerTarget = "tools" | "work" | "output";
const executionLabels = {
	idle: "Нет активных операций",
	queued: "Есть только ожидающие операции",
	running: "Выполняется операция",
	claimed: "Операция уже захвачена обработчиком",
} as const;

export function SettingsScreen({
	settings,
	onSettings,
	onOutputReset,
	workDirectoryBusy = false,
}: SettingsScreenProps) {
	const [picker, setPicker] = useState<PickerTarget | null>(null);
	const [notice, setNotice] = useState("");
	const [confirming, setConfirming] = useState<string | null>(null);
	const currentOutput = settings.currentOutput ?? "/srv/music";
	const publicationFormat = settings.publicationFormat ?? "";
	const musicBrainzMode = settings.musicBrainzMode ?? "";
	const musicBrainzURL = settings.musicBrainzURL ?? "";
	const musicBrainzThrottle = settings.musicBrainzThrottle ?? false;
	const musicBrainzDelay = settings.musicBrainzDelay ?? "";
	const musicBrainzVerified = settings.musicBrainzVerified ?? false;
	const lrclibEnabled = settings.lrclibEnabled ?? true;
	const sha256Enabled = settings.sha256Enabled ?? true;
	const concurrency = settings.concurrency ?? "4";
	const logLevel = settings.logLevel ?? "info";
	const platformMismatch = settings.platformMismatch ?? false;
	const configurationProblem = settings.configurationProblem ?? false;
	const installations = settings.installations ?? initialDemoInstallations;
	const selectedRelease = settings.selectedRelease ?? {};
	const catalog = settings.toolCatalog ?? availableToolReleases;
	const [catalogCheckedAt, setCatalogCheckedAt] = useState(() => {
		try {
			return window.localStorage.getItem("demo-tool-catalog-checked-at") ?? "";
		} catch {
			return "";
		}
	});
	const [moveCandidate, setMoveCandidate] = useState("");
	const [movePlan, setMovePlan] = useState<ReturnType<
		typeof movePreflightDemo
	> | null>(null);
	const [moveConflictsAcknowledged, setMoveConflictsAcknowledged] =
		useState(false);
	const [simulateMoveFailure, setSimulateMoveFailure] = useState(false);
	const [simulateMoveConflict, setSimulateMoveConflict] = useState(false);
	const [simulateInstallFailure, setSimulateInstallFailure] = useState(false);
	const [simulateInstallConflict, setSimulateInstallConflict] = useState(false);
	const [installPlan, setInstallPlan] = useState<{
		kind: ManagedToolPackage;
		release: string;
		root: string;
		targets: string[];
		token: string;
	} | null>(null);
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	useEffect(() => {
		if (platformMismatch) return;
		const checkedAt = Date.parse(catalogCheckedAt);
		if (
			Number.isFinite(checkedAt) &&
			Date.now() - checkedAt < 24 * 60 * 60 * 1000
		)
			return;
		const now = new Date().toISOString();
		try {
			window.localStorage.setItem("demo-tool-catalog-checked-at", now);
		} catch {
			// The demo catalog remains usable when browser storage is unavailable.
		}
		setCatalogCheckedAt(now);
		update({ toolCatalog: availableToolReleases });
	}, [platformMismatch]);
	const blocked = ["running", "claimed"].includes(settings.outputDemoExecution);
	const samePath =
		!settings.outputCandidate || settings.outputCandidate === currentOutput;
	const candidate = settings.outputCandidate;
	const managedCount = installations.reduce(
		(count, item) => count + (item.package === "ffmpeg" ? 2 : 1),
		0,
	);

	const requestOutputReset = () => {
		if (!candidate || candidate === currentOutput) {
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
				"Подтверждение устарело: есть выполняющаяся или захваченная операция. Смена не выполнена.",
			);
			return;
		}
		if (settings.outputCandidate !== confirming) {
			setConfirming(null);
			setNotice(
				"Подтверждение устарело: параметры изменились. Смена не выполнена.",
			);
			return;
		}
		const nextPath = confirming;
		if (!nextPath) return;
		update({ currentOutput: nextPath });
		setConfirming(null);
		onOutputReset?.(nextPath);
		setNotice(
			"Демо: каталог сменён. Очереди и текущие публикации сброшены; инвентарь, рабочие копии и незавершённые шаги сохранены. Файлы не удалялись; автоматической повторной публикации нет.",
		);
	};

	const refreshCatalog = () => {
		if (platformMismatch) {
			setNotice(
				"Платформа не совпадает: platform-dependent refresh/install/activation actions отключены.",
			);
			return;
		}
		const now = new Date().toISOString();
		try {
			window.localStorage.setItem("demo-tool-catalog-checked-at", now);
		} catch {
			// Manual refresh still works without persistent browser storage.
		}
		setCatalogCheckedAt(now);
		update({ toolCatalog: availableToolReleases });
		setNotice(
			"Демо: каталог запрошен вручную с обходом 24-часового автоматического интервала. Ничего не скачано и не активировано.",
		);
	};
	const installTargets = (
		kind: ManagedToolPackage,
		release: string,
		root: string,
	) =>
		setupToolExecutables(kind).map(
			(executable) =>
				`${root.replace(/\/$/, "")}/${kind}/${release}/bin/${executable}`,
		);
	const requestInstall = (kind: ManagedToolPackage) => {
		const release = selectedRelease[kind];
		if (!release || platformMismatch) return;
		const targets = installTargets(kind, release, settings.toolsPath);
		const token = [kind, release, settings.toolsPath, ...targets].join("|");
		setInstallPlan({ kind, release, root: settings.toolsPath, targets, token });
		setSimulateInstallConflict(false);
	};
	const startInstall = () => {
		if (!installPlan || platformMismatch) return;
		const { kind, release } = installPlan;
		const targets = installTargets(
			kind,
			selectedRelease[kind] ?? "",
			settings.toolsPath,
		);
		const currentToken = [
			kind,
			selectedRelease[kind],
			settings.toolsPath,
			...targets,
		].join("|");
		if (currentToken !== installPlan.token || simulateInstallConflict) {
			setInstallPlan(null);
			setNotice(
				simulateInstallConflict
					? "Демо: целевой путь занят. Активная версия и каталог инструментов не изменены."
					: "Проверка устарела: версия или каталог изменились. Повторите preflight.",
			);
			return;
		}
		if (simulateInstallFailure) {
			setInstallPlan(null);
			setNotice(
				`Демо: проверка ${kind} ${release} завершилась ошибкой. Активная версия сохранена; повторите попытку.`,
			);
			return;
		}
		update({
			installations: createDemoInstallation(installations, kind, release),
		});
		setInstallPlan(null);
		setNotice(
			`Демо: ${kind} ${release} добавлен как verified installed; он не активирован.`,
		);
	};
	const requestMove = () => {
		if (!isAbsoluteDemoServerPath(moveCandidate.trim())) {
			setNotice(
				"Перенос не начат: выберите абсолютный путь каталога инструментов на сервере.",
			);
			return;
		}
		setMovePlan(
			movePreflightDemo(
				moveCandidate,
				managedCount,
				simulateMoveConflict
					? ["demo target already contains a managed path"]
					: [],
			),
		);
		setMoveConflictsAcknowledged(false);
	};
	const confirmMove = () => {
		if (!isAbsoluteDemoServerPath(moveCandidate.trim())) {
			setMovePlan(null);
			setNotice(
				"Путь изменился: требуется абсолютный серверный путь и новая проверка.",
			);
			return;
		}
		if (
			platformMismatch ||
			!movePlan ||
			(movePlan.conflicts.length > 0 && !moveConflictsAcknowledged) ||
			movePlan.token !==
				movePreflightDemo(
					moveCandidate,
					managedCount,
					simulateMoveConflict
						? ["demo target already contains a managed path"]
						: [],
				).token
		) {
			setMovePlan(null);
			setMoveConflictsAcknowledged(false);
			setNotice(
				platformMismatch
					? "Несовпадение платформы: перенос заблокирован."
					: "Preflight устарел или конфликты не подтверждены; повторите проверку.",
			);
			return;
		}
		if (simulateMoveFailure) {
			setMovePlan(null);
			setMoveConflictsAcknowledged(false);
			setNotice(
				"Демо: перенос завершился ошибкой. Tools root не изменён; можно повторить preflight.",
			);
			return;
		}
		update({ toolsPath: movePlan.path, pathChecked: false, setupVerified: {} });
		setMovePlan(null);
		setPicker(null);
		setNotice(
			"Демо: перенос завершён успешно; tools root изменён только после завершения операции.",
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
						Демонстрационный макет: значения разделяются с Setup, но не
						сохраняются сервером. Сеть и серверные файлы не затрагиваются.
					</p>
				</div>
				<Button isDisabled={platformMismatch} onPress={refreshCatalog}>
					Обновить каталог вручную
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
					<TextField
						className={styles.formField}
						isReadOnly
						value={currentOutput}
					>
						<Label>Текущий каталог публикации</Label>
						<Input />
					</TextField>
					<small>Текущий путь-макет; продуктовый default не утверждён.</small>
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
							Выбор папки только задаёт кандидата. Отмена ничего не меняет.
						</small>
					</div>
					<Select
						selectedKey={settings.outputDemoExecution}
						onSelectionChange={(key) =>
							key &&
							update({
								outputDemoExecution: String(
									key,
								) as DemoSettings["outputDemoExecution"],
							})
						}
					>
						<Label>Состояние выполнения · демонстрационный сценарий</Label>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								{Object.entries(executionLabels).map(([key, label]) => (
									<ListBoxItem id={key} key={key}>
										{label}
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
					<p role={blocked ? "alert" : "note"}>
						{blocked
							? `Смена заблокирована: ${executionLabels[settings.outputDemoExecution].toLocaleLowerCase()}.`
							: settings.outputDemoExecution === "queued"
								? "Ожидающие задачи не блокируют смену; все очереди сбрасываются при подтверждении."
								: "Активных операций нет в выбранном demo-сценарии."}
					</p>
					<div className="grid gap-2">
						<Select
							selectedKey={publicationFormat || null}
							onSelectionChange={(key) =>
								update({
									publicationFormat: key
										? (String(key) as NonNullable<
												DemoSettings["publicationFormat"]
											>)
										: "",
								})
							}
						>
							<Label>Формат публикации · выбор текущего runtime-значения</Label>
							<Button>
								<SelectValue />
							</Button>
							<Popover>
								<ListBox>
									<ListBoxItem id="mka">MKA</ListBoxItem>
									<ListBoxItem id="original">Исходный формат</ListBoxItem>
								</ListBox>
							</Popover>
						</Select>
						<small>
							MKA — настроенное значение тестовых данных, не продуктовый
							default. Можно явно выбрать исходный формат для новых публикаций.
						</small>
					</div>
					<div className="mt-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3">
						<strong>Эффект смены</strong>
						<ul>
							<li>
								Очереди сбрасываются; running/claimed блокируют смену, queued —
								нет.
							</li>
							<li>
								Текущие публикации и ссылки на старый output сбрасываются;
								физические файлы не удаляются.
							</li>
							<li>Инвентарь, связи и analyses сохраняются.</li>
							<li>
								Рабочая папка и сохранённые AUDIO-копии, их учёт и незавершённые
								шаги сохраняются. Сброшенная обработка автоматически не
								возобновляется.
							</li>
							<li>Автоматической повторной публикации нет.</li>
						</ul>
					</div>
					<Button isDisabled={blocked || samePath} onPress={requestOutputReset}>
						Подтвердить смену каталога публикации
					</Button>
					{samePath && candidate && (
						<p>Кандидат совпадает с текущим путём: менять нечего.</p>
					)}
				</section>
				<section className={styles.panel}>
					<h2>Рабочая папка анализа источников</h2>
					<TextField
						className={styles.formField}
						value={settings.workDirectory}
						isReadOnly
					>
						<Label>Общая папка рабочих копий аудио</Label>
						<div className={styles.objectActions}>
							<Input aria-describedby="settings-source-work-help" />
							<Button
								isDisabled={workDirectoryBusy}
								onPress={() => setPicker("work")}
							>
								Выбрать папку
							</Button>
						</div>
						<small id="settings-source-work-help">
							Серверный путь, отдельный от output.{" "}
							{settings.workDirectory
								? `Текущий выбор: ${settings.workDirectory}.`
								: "Не задана; default отсутствует."}
						</small>
					</TextField>
					<p>
						Требуется перед staged processing; Setup и регистрация root её не
						требуют. Изменение запрещено при использовании или сохранённых
						копиях; автоматического переноса нет.
					</p>
					<p>
						{workDirectoryBusy
							? "Сейчас занята: изменение заблокировано."
							: "Макет не сообщает о live work или retained copies."}
					</p>
				</section>
				<section className={styles.panel}>
					<h2>Runtime и диагностика</h2>
					<p>
						Concurrency 4, SHA-256 включён, cleanup выключен и log level info —
						утверждённые значения по умолчанию. Для остальных неутверждённых
						настроек продуктовый default не задан.
					</p>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={platformMismatch}
							onChange={(event) =>
								update({ platformMismatch: event.target.checked })
							}
						/>
						Демо: платформа БД не совпадает с текущей
					</label>
					<p
						role={platformMismatch || configurationProblem ? "alert" : "status"}
					>
						{platformMismatch
							? "Несовпадение платформы: tool catalog/install/activate/delete/move отключены; обычные настройки видны. Автоматической миграции нет."
							: configurationProblem
								? "Проблема конфигурации в demo: выбранные инструменты недоступны. Проверьте tools lifecycle."
								: "Платформенная диагностика: совпадает · demo; configuration health без проблем."}
					</p>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={configurationProblem}
							onChange={(event) =>
								update({ configurationProblem: event.target.checked })
							}
						/>
						Демо: показать configuration health problem
					</label>
					<Select
						selectedKey={musicBrainzMode || null}
						onSelectionChange={(key) => {
							update({
								musicBrainzMode: key
									? (String(key) as "public" | "self-hosted")
									: "",
								musicBrainzVerified: false,
							});
						}}
					>
						<Label>MusicBrainz · конфигурация</Label>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								<ListBoxItem id="public">Публичный сервис</ListBoxItem>
								<ListBoxItem id="self-hosted">Собственный сервер</ListBoxItem>
							</ListBox>
						</Popover>
					</Select>
					{musicBrainzMode === "self-hosted" && (
						<TextField
							value={musicBrainzURL}
							onChange={(value) => {
								update({ musicBrainzURL: value, musicBrainzVerified: false });
							}}
						>
							<Label>Базовый URL собственного сервера</Label>
							<Input type="url" />
						</TextField>
					)}
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={musicBrainzThrottle}
							disabled={musicBrainzMode !== "self-hosted"}
							onChange={(event) =>
								update({ musicBrainzThrottle: event.target.checked })
							}
						/>
						Ограничивать запросы к собственному серверу
					</label>
					<TextField
						value={musicBrainzDelay}
						onChange={(musicBrainzDelay) => update({ musicBrainzDelay })}
					>
						<Label>Задержка self-hosted запросов · секунды</Label>
						<Input type="number" min="0" />
					</TextField>
					<Button
						isDisabled={
							!musicBrainzMode ||
							(musicBrainzMode === "self-hosted" && !musicBrainzURL.trim())
						}
						onPress={() => {
							update({ musicBrainzVerified: true });
							setNotice(
								"Демо: проверка MusicBrainz отмечена успешной; реальный запрос не выполнялся.",
							);
						}}
					>
						Проверить MusicBrainz
					</Button>
					<p>{musicBrainzVerified ? "Проверено в demo" : "Не проверено"}</p>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={lrclibEnabled}
							onChange={(event) =>
								update({ lrclibEnabled: event.target.checked })
							}
						/>
						Использовать LRCLIB
					</label>
					<small>
						При выключении новые запросы текстов в LRCLIB не выполняются.
						Сохранённые тексты и текущие публикации остаются без изменений;
						автоматической публикации и обхода ручной защиты нет.
					</small>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={sha256Enabled}
							onChange={(event) =>
								update({ sha256Enabled: event.target.checked })
							}
						/>
						Вычислять SHA-256 источников
					</label>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={settings.sourceCleanupEnabled ?? false}
							onChange={(event) =>
								update({ sourceCleanupEnabled: event.target.checked })
							}
						/>
						Разрешить удаление исходного файла после успешной публикации
					</label>
					<small>
						Выключено по умолчанию. Включение разрешает удаление оригинала после
						успешной публикации; оно не удаляет рабочие копии автоматически, не
						меняет текущие публикации и не переопределяет ручную защиту.
					</small>
					<TextField
						value={concurrency}
						onChange={(concurrency) => update({ concurrency })}
					>
						<Label>Параллельность обработки файлов</Label>
						<Input type="number" min="1" />
					</TextField>
					<Select
						selectedKey={logLevel || null}
						onSelectionChange={(key) =>
							update({
								logLevel: key
									? (String(key) as NonNullable<DemoSettings["logLevel"]>)
									: "",
							})
						}
					>
						<Label>Уровень журнала</Label>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								{["debug", "info", "warn", "error"].map((level) => (
									<ListBoxItem id={level} key={level}>
										{level}
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
					<Button
						isDisabled={
							!musicBrainzMode ||
							(musicBrainzMode === "self-hosted" && !musicBrainzURL.trim()) ||
							!publicationFormat ||
							!logLevel
						}
						onPress={() => {
							update({ runtimeSaved: true });
							setNotice(
								"Runtime settings сохранены в демонстрационное состояние; backend не вызывался.",
							);
						}}
					>
						Сохранить runtime settings · демо
					</Button>
					<p>
						{settings.runtimeSaved
							? "Настройки сохранены в памяти макета."
							: "Runtime draft ещё не сохранён."}
					</p>
				</section>
				<section className={styles.panel}>
					<h2>Управляемые инструменты</h2>
					<p>
						Каталог в памяти клиента; успешное время проверки хранится в
						localStorage в продукте. Автоматический запрос не чаще раза в 24
						часа. Ручное обновление выше всегда обходит cooldown и никогда не
						устанавливает/активирует.
					</p>
					<p>
						Последняя успешная проверка:{" "}
						{catalogCheckedAt
							? new Date(catalogCheckedAt).toLocaleString()
							: "не выполнялась"}
					</p>
					<Button isDisabled={platformMismatch} onPress={refreshCatalog}>
						Обновить каталог · ручной обход интервала
					</Button>
					{(["ffmpeg", "fpcalc"] as const).map((kind) => (
						<section
							className="grid gap-2 border-t border-[var(--line)] py-3"
							key={kind}
						>
							<h3 className="m-0 font-semibold">
								{kind === "ffmpeg"
									? "FFmpeg package · ffmpeg + ffprobe"
									: "fpcalc"}
							</h3>
							<ul>
								{installations
									.filter((item) => item.package === kind)
									.map((item) => (
										<li key={item.id}>
											<strong>{item.release}</strong> ·{" "}
											{item.status === "active" ? "active" : "installed"}
											{item.occupied ? " · занята операцией" : ""}
											{item.status === "installed" && (
												<span className="ml-2 inline-flex gap-2">
													<Button
														isDisabled={platformMismatch}
														onPress={() =>
															update({
																installations: installations.map(
																	(candidateItem) =>
																		candidateItem.id === item.id
																			? {
																					...candidateItem,
																					occupied: !candidateItem.occupied,
																				}
																			: candidateItem,
																),
															})
														}
													>
														{item.occupied
															? "Освободить в демо"
															: "Занять в демо"}
													</Button>
													<Button
														isDisabled={platformMismatch || item.occupied}
														onPress={() =>
															update({
																installations: activateDemoInstallation(
																	installations,
																	item.id,
																),
															})
														}
													>
														Активировать
													</Button>
													<Button
														isDisabled={
															platformMismatch ||
															!canDeleteDemoInstallation(item)
														}
														onPress={() =>
															update({
																installations: installations.filter(
																	(candidateItem) =>
																		candidateItem.id !== item.id,
																),
															})
														}
													>
														Удалить inactive
													</Button>
												</span>
											)}
										</li>
									))}
							</ul>
							<Select
								selectedKey={selectedRelease[kind] ?? null}
								onSelectionChange={(key) =>
									update({
										selectedRelease: {
											...selectedRelease,
											[kind]: key ? String(key) : undefined,
										},
									})
								}
							>
								<Label>Выбрать доступную версию · {kind}</Label>
								<Button>
									<SelectValue />
								</Button>
								<Popover>
									<ListBox>
										{catalog[kind].map((release) => (
											<ListBoxItem
												id={release}
												key={release}
												textValue={`${release} · demo`}
											>
												{release} · demo
											</ListBoxItem>
										))}
									</ListBox>
								</Popover>
							</Select>
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={simulateInstallFailure}
									onChange={(event) =>
										setSimulateInstallFailure(event.target.checked)
									}
								/>
								Демо: имитировать ошибку install/verification
							</label>
							<Button
								isDisabled={platformMismatch || !selectedRelease[kind]}
								onPress={() => requestInstall(kind)}
							>
								Подготовить установку · оставить неактивной
							</Button>
							<p className="m-0">
								В продукте сверяются опубликованные SHA-256 (если есть) и каждый
								executable через <code>-version</code>. Здесь реальные загрузка
								и проверка не выполняются; ошибка не меняет active в demo.
							</p>
						</section>
					))}
					<h3>Перенос tools root</h3>
					<p>
						Изменение root доступно только через move operation. Managed files:{" "}
						{managedCount}.
					</p>
					<div className={styles.objectActions}>
						<TextField value={moveCandidate} onChange={setMoveCandidate}>
							<Label>Новый tools root · кандидат</Label>
							<Input placeholder="Явно укажите каталог" />
						</TextField>
						<Button isDisabled={platformMismatch} onPress={requestMove}>
							Выполнить preflight переноса
						</Button>
					</div>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={simulateMoveFailure}
							onChange={(event) => setSimulateMoveFailure(event.target.checked)}
						/>
						Демо: имитировать ошибку переноса
					</label>
					<label className="flex items-center gap-2">
						<input
							type="checkbox"
							checked={simulateMoveConflict}
							onChange={(event) => {
								setSimulateMoveConflict(event.target.checked);
								setMovePlan(null);
							}}
						/>
						Демо: показать конфликт preflight
					</label>
					<p>
						Текущий root: <code>{settings.toolsPath}</code>. Кандидат не меняет
						путь до успешного подтверждения.
					</p>
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
								: settings.outputCandidate || currentOutput
					}
					onClose={() => setPicker(null)}
					onChoose={(path) => {
						if (picker === "tools") {
							setMoveCandidate(path);
							setMovePlan(null);
							setNotice(
								"Кандидат tools root выбран; текущий root не изменён. Выполните preflight.",
							);
						} else if (picker === "work") {
							update({ workDirectory: path });
							setNotice(
								"Демо: рабочая папка выбрана и сохранена в памяти. Файлы и занятость не проверялись.",
							);
						} else {
							update({ outputCandidate: path });
							setNotice(
								`Демо: выбран кандидат ${path}; текущий output не изменён.`,
							);
						}
						setPicker(null);
					}}
				/>
			)}
			{confirming && (
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
							<strong>
								{currentOutput} → {confirming}
							</strong>
						</p>
						<p>
							Queued допускается, все очереди сбрасываются. Текущие публикации и
							старые output references очищаются, физические файлы не удаляются;
							staged copies, общий workDirectory и незавершённые шаги
							сохраняются. Автоматического republish/resume нет.
						</p>
						<p>Это демонстрация: сервер и файлы не изменяются.</p>
						<Button onPress={confirmOutputReset}>Подтвердить смену</Button>
						<Button onPress={() => setConfirming(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
			{installPlan && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setInstallPlan(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">
							Проверка установки {installPlan.kind} · {installPlan.release}
						</Heading>
						<p>
							Корень инструментов: <code>{installPlan.root}</code>. Целевые
							файлы:
						</p>
						<ul>
							{installPlan.targets.map((target) => (
								<li key={target}>
									<code>{target}</code>
								</li>
							))}
						</ul>
						<p>
							Успех создаст проверенную неактивную версию. Активная версия не
							переключается.
						</p>
						<label className="flex items-center gap-2">
							<input
								type="checkbox"
								checked={simulateInstallConflict}
								onChange={(event) =>
									setSimulateInstallConflict(event.target.checked)
								}
							/>
							Демо: смоделировать конфликт целевого пути
						</label>
						<Button
							isDisabled={platformMismatch || simulateInstallConflict}
							onPress={startInstall}
						>
							Подтвердить проверку
						</Button>
						<Button onPress={() => setInstallPlan(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
			{movePlan && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setMovePlan(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">Preflight переноса tools root</Heading>
						<p>
							Цель: <code>{movePlan.path}</code>
						</p>
						<p>
							Managed files: {movePlan.managedCount}. Конфликты:{" "}
							{movePlan.conflicts.length
								? movePlan.conflicts.join(", ")
								: "нет"}
							.
						</p>
						{movePlan.conflicts.length > 0 && (
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={moveConflictsAcknowledged}
									onChange={(event) =>
										setMoveConflictsAcknowledged(event.target.checked)
									}
								/>
								Подтверждаю именно показанные demo conflicts
							</label>
						)}
						<Button
							isDisabled={
								platformMismatch ||
								(movePlan.conflicts.length > 0 && !moveConflictsAcknowledged)
							}
							onPress={confirmMove}
						>
							Запустить перенос и изменить root только после успеха
						</Button>
						<Button onPress={() => setMovePlan(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}
