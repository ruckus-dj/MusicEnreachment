import { useState } from "react";
import {
	Button,
	Dialog,
	Heading,
	Input,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Modal,
	TextField,
} from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import type { DemoSettings, SettingsScreenProps } from "./SettingsScreen";
import { formStyles as styles } from "./formStyles";
import {
	activateOrCreateDemoInstallation,
	isAbsoluteDemoServerPath,
	type ManagedToolPackage,
} from "./ToolLifecycleDemo";
import { setupToolExecutables, setupToolReleases } from "./setupToolsLifecycle";

const steps = ["Каталог инструментов", "Инструменты", "Публикация", "Итог"];

export function SetupScreen({
	settings,
	onSettings,
	step: savedStep,
	onStepChange,
}: SettingsScreenProps & {
	step?: number;
	onStepChange?: (step: number) => void;
}) {
	const [localStep, setLocalStep] = useState(0);
	const step = savedStep ?? localStep;
	const setStep = (next: number) => {
		setLocalStep(next);
		onStepChange?.(next);
	};
	const [pickerOpen, setPickerOpen] = useState(false);
	const [outputPickerOpen, setOutputPickerOpen] = useState(false);
	const [notice, setNotice] = useState("");
	const [pathError, setPathError] = useState(false);
	const [installTarget, setInstallTarget] = useState<ManagedToolPackage | null>(
		null,
	);
	const [installConflict, setInstallConflict] = useState(false);
	const [installToken, setInstallToken] = useState("");
	const update = (values: Partial<DemoSettings>) =>
		onSettings({ ...settings, ...values });
	const toolSelection = settings.selectedRelease ?? {};
	const installations = settings.installations ?? [];
	const activeInstallation = (kind: ManagedToolPackage) =>
		installations.find(
			(item) => item.package === kind && item.status === "active",
		);
	const toolsReady = (["ffmpeg", "fpcalc"] as const).every((kind) =>
		Boolean(
			toolSelection[kind] &&
				settings.setupVerified?.[kind] === toolSelection[kind] &&
				activeInstallation(kind)?.release === toolSelection[kind],
		),
	);
	const platformMismatch = settings.platformMismatch ?? false;
	const musicBrainzMode = settings.musicBrainzMode ?? "";
	const musicBrainzURL = settings.musicBrainzURL ?? "";
	const musicBrainzChecked = settings.musicBrainzVerified ?? false;
	const simulateToolFailure = settings.simulateToolFailure ?? false;
	const simulateConnectionFailure = settings.simulateConnectionFailure ?? false;
	const outputPath = settings.outputCandidate;
	const outputChecked = settings.outputChecked ?? false;
	const simulateOutputNotEmpty = settings.simulateOutputNotEmpty ?? false;
	const musicBrainzReady =
		musicBrainzChecked &&
		Boolean(musicBrainzMode) &&
		(musicBrainzMode === "public" || Boolean(musicBrainzURL.trim()));
	const publicationReady = outputChecked && musicBrainzReady;

	const checkPath = () => {
		const path = settings.toolsPath.trim();
		const accepted = isAbsoluteDemoServerPath(path);
		update({ toolsPath: path, pathChecked: accepted });
		setPathError(!accepted);
		setNotice(
			accepted
				? "Путь принят в демонстрации; права, постоянный том и файловая система не проверялись."
				: "Выберите абсолютный серверный путь перед проверкой.",
		);
	};
	const installTool = (kind: ManagedToolPackage) => {
		const release = toolSelection[kind];
		if (!release || platformMismatch) return;
		const targets = setupToolExecutables(kind).map(
			(executable) =>
				`${settings.toolsPath.replace(/\/$/, "")}/${kind}/${release}/bin/${executable}`,
		);
		const token = [kind, release, settings.toolsPath, ...targets].join("|");
		if (token !== installToken || installConflict) {
			setInstallTarget(null);
			setNotice(
				installConflict
					? "Демо: целевой путь занят. Активная версия не изменена."
					: "Проверка устарела: версия или каталог изменились. Подготовьте её заново.",
			);
			return;
		}
		if (!simulateToolFailure) {
			update({
				installations: activateOrCreateDemoInstallation(
					installations,
					kind,
					release,
				),
				setupVerified: { ...settings.setupVerified, [kind]: release },
				setupFailures: { ...settings.setupFailures, [kind]: undefined },
			});
		} else {
			update({
				setupFailures: {
					...settings.setupFailures,
					[kind]: `Проверка ${release} завершилась ошибкой`,
				},
			});
		}
		setNotice(
			simulateToolFailure
				? `Демо: проверка ${kind} ${release} завершилась ошибкой. Активная версия не изменена; можно повторить.`
				: `Демо: ${kind} ${release} проверен; опубликованный SHA-256 сверяется, если он есть, а каждый executable проверяется через -version. Активная версия меняется только после успеха.`,
		);
		setInstallTarget(null);
		setInstallConflict(false);
	};
	const checkMusicBrainz = () => {
		if (
			!musicBrainzMode ||
			(musicBrainzMode === "self-hosted" && !musicBrainzURL.trim())
		) {
			update({ musicBrainzVerified: false });
			setNotice(
				"Выберите публичный MusicBrainz или укажите адрес собственного сервера перед проверкой.",
			);
			return;
		}
		update({ musicBrainzVerified: !simulateConnectionFailure });
		setNotice(
			simulateConnectionFailure
				? "Демо: проверка MusicBrainz не удалась. Исправьте параметры и повторите проверку."
				: "Демо: подключение MusicBrainz отмечено как проверенное; сетевой запрос не выполнялся.",
		);
	};
	const chooseToolsPath = (path: string) => {
		setPathError(false);
		update({ toolsPath: path, pathChecked: false, setupVerified: {} });
		setNotice(
			"Демо: выбран серверный каталог. Проверка прав и конфигурации не выполнялась.",
		);
		setPickerOpen(false);
	};
	const chooseOutputPath = (path: string) => {
		update({ outputCandidate: path, outputChecked: false });
		setOutputPickerOpen(false);
		setNotice(
			"Демо: выбран кандидат каталога публикации. Текущий каталог не менялся; файловая система не проверялась.",
		);
	};
	const next = () => {
		if (platformMismatch) {
			setNotice(
				"Продолжение Setup заблокировано из-за несовпадения платформы.",
			);
			return;
		}
		if (!canContinue) return;
		if (step === 3) {
			if (!settings.pathChecked || !toolsReady || !publicationReady) {
				setNotice(
					"Проверки Setup изменились. Вернитесь и повторите обязательные проверки.",
				);
				return;
			}
			update({ currentOutput: outputPath });
			window.location.hash = "review";
		} else setStep(Math.min(step + 1, 3));
	};
	const canContinue =
		step === 0
			? settings.pathChecked
			: step === 1
				? toolsReady
				: step === 2
					? publicationReady
					: true;
	const canProceed = canContinue && !platformMismatch;

	return (
		<section className={styles.page} aria-labelledby="setup-title">
			<header className={styles.pageHeader}>
				<h1 className={styles.pageTitle} id="setup-title">
					Первый запуск
				</h1>
				<p className={styles.pageDescription}>
					Демонстрационный макет: изменения только в памяти страницы. Сеть,
					загрузка файлов и серверная файловая система не используются.
				</p>
			</header>
			{platformMismatch && (
				<p className={styles.formNotice} role="alert">
					Платформа базы данных не совпадает с текущей. Действия Setup и
					продолжение заблокированы.
				</p>
			)}
			<nav aria-label="Шаги первого запуска">
				<ol className={styles.setupSteps}>
					{steps.map((label, index) => (
						<li aria-current={index === step ? "step" : undefined} key={label}>
							<span className={styles.setupStepIndex}>
								{index < step ? "✓" : index + 1}
							</span>
							<span>{label}</span>
						</li>
					))}
				</ol>
			</nav>
			<section className={styles.panel} aria-labelledby="setup-step-title">
				<h2 className="mb-2 text-sm font-semibold" id="setup-step-title">
					{steps[step]}
				</h2>
				<div className="grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-[14px]">
					{step === 0 && (
						<>
							<TextField
								className={styles.searchField}
								value={settings.toolsPath}
								isReadOnly
							>
								<Label>Постоянный каталог инструментов</Label>
								<div className={styles.objectActions}>
									<Input
										id="tools-path"
										aria-invalid={pathError}
										aria-describedby="path-help"
									/>
									<Button onPress={() => setPickerOpen(true)}>
										Выбрать папку
									</Button>
								</div>
								<small id="path-help">
									Абсолютный путь на сервере. Выбор требуется подтвердить
									проверкой.
								</small>
							</TextField>
							<Button isDisabled={platformMismatch} onPress={checkPath}>
								Проверить путь
							</Button>
							{settings.pathChecked && (
								<p role="status">
									Путь отмечен как проверенный в демо; доступ и постоянное
									хранилище не подтверждены.
								</p>
							)}
						</>
					)}
					{step === 1 && (
						<>
							<p>
								Выберите numbered release для каждого пакета и явно запустите
								демонстрационную проверку. FFmpeg package содержит ffmpeg и
								ffprobe.
							</p>
							<label className="flex items-center gap-2">
								<input
									type="checkbox"
									checked={simulateToolFailure}
									onChange={(event) =>
										update({ simulateToolFailure: event.target.checked })
									}
								/>
								Демо: имитировать ошибку загрузки/проверки
							</label>
							{(["ffmpeg", "fpcalc"] as const).map((kind) => (
								<section
									className="grid gap-2 rounded-md border border-[var(--line)] p-3"
									key={kind}
									aria-labelledby={`setup-${kind}-title`}
								>
									<h3 id={`setup-${kind}-title`} className="m-0 font-semibold">
										{kind === "ffmpeg"
											? "FFmpeg package · ffmpeg + ffprobe"
											: "fpcalc"}
									</h3>
									<Select
										selectedKey={toolSelection[kind] ?? null}
										onSelectionChange={(key) => {
											update({
												selectedRelease: {
													...toolSelection,
													[kind]: key ? String(key) : undefined,
												},
												setupFailures: {
													...settings.setupFailures,
													[kind]: undefined,
												},
											});
										}}
									>
										<Label>Выбрать release · {kind}</Label>
										<Button>
											<SelectValue />
										</Button>
										<Popover>
											<ListBox>
												{setupToolReleases[kind].map((release) => (
													<ListBoxItem
														id={release}
														key={release}
														textValue={`${release} · demo release`}
													>
														{release} · demo release
													</ListBoxItem>
												))}
											</ListBox>
										</Popover>
									</Select>
									<p className="m-0">
										Проверяемые executable:{" "}
										{setupToolExecutables(kind).join(", ")}. SHA-256 — если
										опубликован; каждому executable передаётся{" "}
										<code>-version</code>.
									</p>
									<p className="m-0">
										Состояние:{" "}
										{settings.setupFailures?.[kind]
											? `ошибка · ${settings.setupFailures[kind]}; active версия не изменена`
											: activeInstallation(kind)
												? `проверен и активен · ${activeInstallation(kind)?.release}`
												: "активная версия отсутствует"}
									</p>
									{settings.setupFailures?.[kind] && (
										<p role="alert">
											Прежняя активная версия не изменена. Отключите имитацию
											ошибки и повторите проверку выбранной версии.
										</p>
									)}
									<Button
										isDisabled={!toolSelection[kind] || platformMismatch}
										onPress={() => {
											setInstallTarget(kind);
											setInstallConflict(false);
											const release = toolSelection[kind];
											if (release) {
												const paths = setupToolExecutables(kind).map(
													(executable) =>
														`${settings.toolsPath.replace(/\/$/, "")}/${kind}/${release}/bin/${executable}`,
												);
												setInstallToken(
													[kind, release, settings.toolsPath, ...paths].join(
														"|",
													),
												);
											}
										}}
									>
										{settings.setupFailures?.[kind]
											? "Повторить проверку выбранной версии"
											: "Подготовить и проверить выбранную версию · демо"}
									</Button>
								</section>
							))}
						</>
					)}
					{step === 2 && (
						<>
							<section className="grid gap-2">
								<h3 className="m-0 font-semibold">Каталог публикации</h3>
								<p>
									Первоначальная настройка требует явно выбранный и проверенный
									пустой каталог. В макете путь не проверяется и содержимое не
									читается.
								</p>
								<div className={styles.objectActions}>
									<TextField value={outputPath} isReadOnly>
										<Label>Выбранный каталог · кандидат</Label>
										<Input placeholder="Не выбран" />
									</TextField>
									<Button onPress={() => setOutputPickerOpen(true)}>
										Выбрать папку
									</Button>
								</div>
								<label className="flex items-center gap-2">
									<input
										type="checkbox"
										checked={simulateOutputNotEmpty}
										onChange={(event) => {
											update({
												simulateOutputNotEmpty: event.target.checked,
												outputChecked: false,
											});
										}}
									/>
									Демо: имитировать непустой каталог
								</label>
								<Button
									isDisabled={!outputPath}
									onPress={() => {
										update({
											outputChecked:
												Boolean(outputPath) && !simulateOutputNotEmpty,
										});
										setNotice(
											!outputPath
												? "Выберите каталог."
												: simulateOutputNotEmpty
													? "Проверка не пройдена: каталог не пуст. Выберите другой путь и повторите проверку."
													: "Демо: кандидат отмечен как пустой и проверенный; реальное содержимое не читалось.",
										);
									}}
								>
									Проверить путь и пустоту · демо
								</Button>
								{simulateOutputNotEmpty && (
									<p role="alert">
										Каталог отмечен непустым в demo; Setup нельзя продолжить.
									</p>
								)}
								{outputChecked && (
									<p role="status">
										Путь отмечен проверенным только в демонстрации; фактическая
										пустота не подтверждена.
									</p>
								)}
							</section>
							<section className="grid gap-2">
								<h3 className="m-0 font-semibold">
									Подключение MusicBrainz · обязательно
								</h3>
								<p>
									Явно выберите публичный сервис или собственное размещение. Для
									собственного сервера требуется адрес.
								</p>
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
									<Label>Источник MusicBrainz · выбор обязателен</Label>
									<Button>
										<SelectValue />
									</Button>
									<Popover>
										<ListBox>
											<ListBoxItem id="public">
												Публичный MusicBrainz
											</ListBoxItem>
											<ListBoxItem id="self-hosted">
												Собственный сервер
											</ListBoxItem>
										</ListBox>
									</Popover>
								</Select>
								{musicBrainzMode === "self-hosted" && (
									<TextField
										value={musicBrainzURL}
										onChange={(value) => {
											update({
												musicBrainzURL: value,
												musicBrainzVerified: false,
											});
										}}
									>
										<Label>Базовый URL собственного MusicBrainz</Label>
										<Input
											type="url"
											placeholder="https://musicbrainz.example"
										/>
									</TextField>
								)}
								<label className="flex items-center gap-2">
									<input
										type="checkbox"
										checked={simulateConnectionFailure}
										onChange={(event) => {
											update({
												simulateConnectionFailure: event.target.checked,
												musicBrainzVerified: false,
											});
										}}
									/>
									Демо: имитировать ошибку соединения
								</label>
								<Button
									isDisabled={
										!musicBrainzMode ||
										(musicBrainzMode === "self-hosted" &&
											!musicBrainzURL.trim())
									}
									onPress={checkMusicBrainz}
								>
									{musicBrainzChecked
										? "Повторить проверку MusicBrainz"
										: "Проверить соединение MusicBrainz"}
								</Button>
								{musicBrainzChecked && (
									<p role="status">Соединение отмечено проверенным в демо.</p>
								)}
							</section>
							<section className="grid gap-2">
								<h3 className="m-0 font-semibold">
									Библиотека жанров · необязательно для продолжения
								</h3>
								<GenreLibrary
									loaded={settings.genresLoaded}
									onLoadedChange={(genresLoaded) => update({ genresLoaded })}
								/>
							</section>
						</>
					)}
					{step === 3 && (
						<dl className="my-[10px] grid grid-cols-[minmax(100px,1fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] [&_dd]:[overflow-wrap:anywhere] [&_dd]:my-0 [&_dt]:text-[var(--muted)]">
							<div>
								<dt>Инструменты</dt>
								<dd>FFmpeg package и fpcalc · проверены и активны в demo</dd>
							</div>
							<div>
								<dt>Каталог публикации</dt>
								<dd>
									{outputPath} · кандидат, в реальном Setup проверка пустоты
									обязательна
								</dd>
							</div>
							<div>
								<dt>MusicBrainz</dt>
								<dd>
									{musicBrainzMode === "public"
										? "Публичный сервис"
										: `Собственный сервер · ${musicBrainzURL}`}
								</dd>
							</div>
							<div>
								<dt>Библиотека жанров</dt>
								<dd>
									{settings.genresLoaded
										? "Демо-заполнена"
										: "Не загружена · не блокирует Setup"}
								</dd>
							</div>
						</dl>
					)}
					{notice && (
						<p
							className="my-2 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px]"
							role="status"
							aria-live="polite"
						>
							{notice}
						</p>
					)}
					<div className={styles.setupFooter}>
						{step > 0 && (
							<Button onPress={() => setStep(step - 1)}>Назад</Button>
						)}
						<Button isDisabled={!canProceed} onPress={next}>
							{step === 3 ? "Завершить демонстрацию" : "Продолжить"}
						</Button>
					</div>
				</div>
			</section>
			{pickerOpen && (
				<DirectoryPicker
					key="setup-tools"
					initialPath={settings.toolsPath || "/"}
					onChoose={chooseToolsPath}
					onClose={() => setPickerOpen(false)}
				/>
			)}
			{outputPickerOpen && (
				<DirectoryPicker
					key="setup-output"
					initialPath={outputPath || "/"}
					onChoose={chooseOutputPath}
					onClose={() => setOutputPickerOpen(false)}
				/>
			)}
			{installTarget && toolSelection[installTarget] && (
				<Modal
					isOpen
					onOpenChange={(open) => !open && setInstallTarget(null)}
					isDismissable
				>
					<Dialog>
						<Heading slot="title">Проверка выбранной версии</Heading>
						<p>
							Пакет {installTarget} · версия {toolSelection[installTarget]}.
							Целевые файлы:
						</p>
						<ul>
							{setupToolExecutables(installTarget).map((executable) => (
								<li key={executable}>
									<code>{`${settings.toolsPath.replace(/\/$/, "")}/${installTarget}/${toolSelection[installTarget]}/bin/${executable}`}</code>
								</li>
							))}
						</ul>
						<p>
							В реальном процессе проверяются опубликованный SHA-256, если он
							доступен, и каждый executable через <code>-version</code>. Демо не
							загружает и не меняет серверные файлы.
						</p>
						<label className="flex items-center gap-2">
							<input
								type="checkbox"
								checked={installConflict}
								onChange={(event) => setInstallConflict(event.target.checked)}
							/>
							Демо: явно подтвердить конфликт целевого пути
						</label>
						{installConflict && (
							<p role="alert">
								Целевой путь занят в выбранном демонстрационном сценарии;
								проверка не запускается.
							</p>
						)}
						<Button
							isDisabled={platformMismatch}
							onPress={() => installTool(installTarget)}
						>
							Подтвердить проверку версии
						</Button>
						<Button onPress={() => setInstallTarget(null)}>Отмена</Button>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}
