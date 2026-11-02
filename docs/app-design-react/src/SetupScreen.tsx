import { useState } from "react";
import { Button, Input, Label, TextField } from "./ui";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import type { SettingsScreenProps } from "./SettingsScreen";
import { formStyles as styles } from "./formStyles";

const steps = ["Каталог", "Инструменты", "Публикация", "Итог"];

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
	const [notice, setNotice] = useState("");
	const [pathError, setPathError] = useState(false);
	const [toolsPath, setToolsPath] = useState(settings.toolsPath);
	const checkPath = () => {
		const path = toolsPath.trim();
		const accepted = path.startsWith("/") && path.length > 1;
		onSettings({ ...settings, toolsPath: path, pathChecked: accepted });
		setPathError(!accepted);
		setNotice(
			accepted
				? "Абсолютный путь принят в демонстрации. Права и постоянный том не проверялись."
				: "Введите абсолютный путь, например /var/lib/melotrove/tools.",
		);
	};
	const next = () => {
		if (step === 3) window.location.hash = "review";
		else setStep(Math.min(step + 1, 3));
	};
	const chooseToolsPath = (path: string) => {
		setToolsPath(path);
		setPathError(false);
		onSettings({ ...settings, toolsPath: path, pathChecked: false });
		setNotice(
			"Демо: выбран серверный каталог. Проверка прав и конфигурации не выполнялась.",
		);
		setPickerOpen(false);
	};

	return (
		<section className={styles.page} aria-labelledby="setup-title">
			<header className={styles.pageHeader}>
				<h1 className={styles.pageTitle} id="setup-title">
					Первый запуск
				</h1>
				<p className={styles.pageDescription}>
					Требуется рабочая конфигурация инструментов. Версии, доступность и
					пути демонстрационные.
				</p>
			</header>
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
				<div className="min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)] p-[14px]">
					{step === 0 && (
						<>
							<TextField
								className={styles.searchField}
								value={toolsPath}
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
									Абсолютный путь на сервере, не на компьютере с браузером.
								</small>
							</TextField>
							<div className={styles.objectActions}>
								<Button onPress={checkPath}>Проверить путь</Button>
							</div>
						</>
					)}
					{step === 1 && (
						<>
							<div className={styles.previewTable}>
								<table>
									<thead>
										<tr>
											<th>Инструмент</th>
											<th>Версия · демо</th>
											<th>Проверка</th>
										</tr>
									</thead>
									<tbody>
										<tr>
											<td>ffmpeg</td>
											<td>8.0</td>
											<td>SHA-256 если опубликован · -version</td>
										</tr>
										<tr>
											<td>ffprobe</td>
											<td>8.0</td>
											<td>SHA-256 если опубликован · -version</td>
										</tr>
										<tr>
											<td>fpcalc</td>
											<td>1.5.1</td>
											<td>-version</td>
										</tr>
									</tbody>
								</table>
							</div>
							<p className="my-2">
								Каждый исполняемый файл проверяется через -version; SHA-256
								проверяется, только если он опубликован. В этом макете
								скачивания нет.
							</p>
						</>
					)}
					{step === 2 && (
						<>
							<dl className="my-[10px] grid grid-cols-[minmax(100px,1fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] [&_dd]:[overflow-wrap:anywhere] [&_dd]:my-0 [&_dt]:text-[var(--muted)]">
								<div>
									<dt>Каталог</dt>
									<dd>
										<code>/srv/music</code>
									</dd>
								</div>
								<div>
									<dt>Режим примера</dt>
									<dd>MKA · перепаковка без перекодирования</dd>
								</div>
								<div>
									<dt>Исходники</dt>
									<dd>Только чтение</dd>
								</div>
								<div>
									<dt>Граница продукта</dt>
									<dd>
										Также предусмотрен исходный формат; макет демонстрирует MKA
									</dd>
								</div>
							</dl>
							<section className="grid gap-2">
								<h3 className="mb-2 text-sm font-semibold">
									Библиотека жанров · необязательно для продолжения
								</h3>
								<GenreLibrary
									loaded={settings.genresLoaded}
									onLoadedChange={(genresLoaded) =>
										onSettings({ ...settings, genresLoaded })
									}
								/>
							</section>
						</>
					)}
					{step === 3 && (
						<dl className="my-[10px] grid grid-cols-[minmax(100px,1fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] [&_dd]:[overflow-wrap:anywhere] [&_dd]:my-0 [&_dt]:text-[var(--muted)]">
							<div>
								<dt>MusicBrainz</dt>
								<dd>
									Публичный сервис · поддержка собственного размещения в
									спецификации
								</dd>
							</div>
							<div>
								<dt>LRCLIB</dt>
								<dd>Тексты</dd>
							</div>
							<div>
								<dt>Авторизация</dt>
								<dd>Внешний прокси авторизации</dd>
							</div>
							<div>
								<dt>База данных</dt>
								<dd>Подключена · демо</dd>
							</div>
							<div>
								<dt>Частичные релизы</dt>
								<dd>Разрешены</dd>
							</div>
						</dl>
					)}
					{notice && (
						<p
							className="my-3 border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-3 py-[10px]"
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
						<Button
							isDisabled={step === 0 && !settings.pathChecked}
							onPress={next}
						>
							{step === 3 ? "Завершить демонстрацию" : "Продолжить"}
						</Button>
					</div>
				</div>
			</section>
			{pickerOpen && (
				<DirectoryPicker
					key="setup-tools"
					initialPath={toolsPath}
					onChoose={chooseToolsPath}
					onClose={() => setPickerOpen(false)}
				/>
			)}
		</section>
	);
}
