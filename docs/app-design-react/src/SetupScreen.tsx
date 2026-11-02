import { useState } from "react";
import { Button } from "react-aria-components";
import { DirectoryPicker } from "./DirectoryPicker";
import { GenreLibrary } from "./GenreLibrary";
import type { SettingsScreenProps } from "./SettingsScreen";

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
		<section aria-labelledby="setup-title">
			<header>
				<h1 id="setup-title">Первый запуск</h1>
				<p>
					Требуется рабочая конфигурация инструментов. Версии, доступность и
					пути демонстрационные.
				</p>
			</header>
			<nav className="demo-tabs" aria-label="Шаги первого запуска">
				{steps.map((label, index) => (
					<span
						className={index === step ? "active" : undefined}
						aria-current={index === step ? "step" : undefined}
						key={label}
					>
						{index + 1}. {label}
					</span>
				))}
			</nav>
			<section className="demo-panel" aria-labelledby="setup-step-title">
				<h2 id="setup-step-title">{steps[step]}</h2>
				<div className="demo-panel">
					{step === 0 && (
						<>
							<label className="demo-search">
								Постоянный каталог инструментов
								<div className="demo-object-actions">
									<input
										id="tools-path"
										value={toolsPath}
										readOnly
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
							</label>
							<div className="demo-object-actions">
								<Button onPress={checkPath}>Проверить путь</Button>
							</div>
						</>
					)}
					{step === 1 && (
						<>
							<div className="demo-preview-table">
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
							<p>
								Каждый исполняемый файл проверяется через -version; SHA-256
								проверяется, только если он опубликован. В этом макете
								скачивания нет.
							</p>
						</>
					)}
					{step === 2 && (
						<>
							<dl className="demo-facts">
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
							<section className="setup-genre-library">
								<h3>Библиотека жанров · необязательно для продолжения</h3>
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
						<dl className="demo-facts">
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
					{notice && <p role="status">{notice}</p>}
					<div className="demo-object-actions">
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
