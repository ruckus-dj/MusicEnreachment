import { useState } from "react";
import {
	Button,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
} from "./ui";

const targets = [
	["library", "Медиатека", "Частично: списки, выбор, фильтры"],
	[
		"artist/mira",
		"Артист Mira Vale",
		"Перенесены издания, позиции и подтверждение удаления; требуется финальная сверка",
	],
	[
		"release/north",
		"Издание Northern Lines",
		"Частично: свойства, позиции, предпросмотр и запрос удаления; сравнение тегов издания ещё не перенесено",
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
	[
		"review",
		"Входящие",
		"Перенесены группы и демонстрационная автоматика; требуется финальная сверка",
	],
	[
		"match/north",
		"Сопоставление",
		"Перенесены назначения, варианты, поиск композиций и частичная публикация; требуется финальная сверка",
	],
	[
		"local",
		"Собственный каталог",
		"Перенесены ручные записи Local отдельно от медиатеки; требуется финальная сверка",
	],
	[
		"create-release",
		"Ручное создание",
		"Перенесён черновик с носителями, позициями, авторством и исходниками; требуется финальная сверка",
	],
	[
		"sources",
		"Источники",
		"Частично: каталоги, режимы, выбор папки и удаление с сохранением публикаций; черновики входящих ещё не перенесены",
	],
	[
		"operations",
		"Операции",
		"Перенесены текущие ошибки, восстановление и запросы удаления; требуется финальная сверка",
	],
	[
		"settings",
		"Настройки",
		"Перенесены демонстрационные настройки и подтверждение смены каталога; нужна финальная сверка",
	],
	[
		"setup",
		"Первый запуск",
		"Перенесены четыре шага и необязательный каталог жанров; нужна финальная сверка",
	],
	[
		"components",
		"Компоненты",
		"Перенесена витрина; требуется финальная сверка",
	],
] as const;

export function Comparison() {
	const [index, setIndex] = useState(0);
	const [width, setWidth] = useState("1440");
	const [route, title, status] = targets[index];
	return (
		<main className="comparison">
			<header className="comparison-header">
				<h1>Сверка переноса</h1>
				<a href="/">Вернуться в React-прототип</a>
			</header>
			<p>
				Слева — исходный HTML-прототип, справа — отдельный React-прототип. Это
				средство сравнения, не способ реализации новых экранов. Состояние внутри
				окон независимо.
			</p>
			<div className="comparison-filters">
				<label className="form-field">
					Экран
					<Select
						aria-label="Экран"
						selectedKey={targets[index][0]}
						onSelectionChange={(key) => {
							const next = targets.findIndex(([id]) => id === key);
							if (next >= 0) setIndex(next);
						}}
					>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								{targets.map(([id, label]) => (
									<ListBoxItem id={id} key={id}>
										{label}
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
				</label>
				<label className="form-field">
					Ширина каждого окна
					<Select
						aria-label="Ширина каждого окна"
						selectedKey={width}
						onSelectionChange={(key) => key && setWidth(String(key))}
					>
						<Button>
							<SelectValue />
						</Button>
						<Popover>
							<ListBox>
								{["1440", "768", "375"].map((size) => (
									<ListBoxItem id={size} key={size}>
										{size} px
									</ListBoxItem>
								))}
							</ListBox>
						</Popover>
					</Select>
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
					route === "local" ||
					route === "create-release" ||
					route === "components" ||
					route.startsWith("artist/") ||
					route.startsWith("tags/") ||
					route === "sources" ||
					route === "review" ||
					route.startsWith("match/") ||
					route === "operations" ||
					route === "settings" ||
					route === "setup" ||
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
