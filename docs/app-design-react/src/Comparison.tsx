import { useState } from "react";

const targets = [
	["library", "Медиатека", "Частично: списки, выбор, фильтры"],
	["artist/mira", "Артист Mira Vale", "Не достигнут паритет страницы артиста"],
	[
		"release/north",
		"Издание Northern Lines",
		"Частично: свойства, позиции и предпросмотр; удаление и сравнение тегов ещё не перенесены",
	],
	[
		"track/north-t2",
		"Позиция Still Water",
		"Частично: исходники, состояния, теги и одиночный предпросмотр; редкие действия не перенесены",
	],
	[
		"publication-preview",
		"Предпросмотр публикации",
		"Частично: требуется сначала выбрать позиции",
	],
	["review", "Входящие", "Не перенесено"],
	["match/north", "Сопоставление", "Не перенесено"],
	["local", "Собственный каталог", "Не перенесено"],
	["create-release", "Ручное создание", "Не перенесено"],
	["sources", "Источники", "Не перенесено"],
	["operations", "Операции", "Не перенесено"],
	["settings", "Настройки", "Не перенесено"],
	["setup", "Первый запуск", "Не перенесено"],
	["components", "Компоненты", "Не перенесено"],
] as const;

export function Comparison() {
	const [index, setIndex] = useState(0);
	const [width, setWidth] = useState("1440");
	const [route, title, status] = targets[index];
	return (
		<main className="comparison">
			<header>
				<h1>Сверка переноса</h1>
				<a href="/">Вернуться в React-прототип</a>
			</header>
			<p>
				Слева — исходный HTML-прототип, справа — отдельный React-прототип. Это
				средство сравнения, не способ реализации новых экранов. Состояние внутри
				окон независимо.
			</p>
			<div className="demo-filters">
				<label>
					Экран
					<select
						value={index}
						onChange={(event) => setIndex(Number(event.currentTarget.value))}
					>
						{targets.map(([id, label], option) => (
							<option value={option} key={id}>
								{label}
							</option>
						))}
					</select>
				</label>
				<label>
					Ширина каждого окна
					<select
						value={width}
						onChange={(event) => setWidth(event.currentTarget.value)}
					>
						<option value="1440">1440 px</option>
						<option value="768">768 px</option>
						<option value="375">375 px</option>
					</select>
				</label>
			</div>
			<p role="status">{status}</p>
			<div className="comparison-windows">
				<section>
					<h2>Исходник · {title}</h2>
					<iframe
						key={`old:${route}:${width}`}
						title="Исходный HTML-прототип"
						src={`/reference/index.html#${route}`}
						style={{ width: Number(width) }}
					/>
				</section>
				<section>
					<h2>React · {title}</h2>
					{route === "library" ||
					route.startsWith("track/") ||
					route.startsWith("release/") ? (
						<iframe
							key={`new:${route}:${width}`}
							title="Новый React-прототип"
							src={`/?embedded=1#${route}`}
							style={{ width: Number(width) }}
						/>
					) : (
						<p>
							Прямая навигация этого экрана пока не перенесена.{" "}
							<a href="/?embedded=1" target="_blank" rel="noreferrer">
								Открыть текущий React-прототип
							</a>
						</p>
					)}
				</section>
			</div>
		</main>
	);
}
