import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { SetupScreen } from "./SetupScreen";
import type { DemoSettings } from "./SettingsScreen";
import { initialDemoSettings } from "./SettingsScreen";

function StatefulSetup() {
	const [settings, setSettings] = useState(initialDemoSettings);
	return <SetupScreen settings={settings} onSettings={setSettings} />;
}

function chooseToolRelease(kind: "ffmpeg" | "fpcalc", release: string) {
	const packageSection = screen
		.getByRole("heading", {
			name: kind === "ffmpeg" ? "FFmpeg package · ffmpeg + ffprobe" : "fpcalc",
		})
		.closest("section");
	if (!packageSection)
		throw new Error(`${kind} package section was not rendered`);
	fireEvent.click(
		within(packageSection).getByRole("button", {
			name: new RegExp(`Выбрать release · ${kind}`),
		}),
	);
	fireEvent.click(
		screen.getByRole("option", { name: `${release} · demo release` }),
	);
	fireEvent.click(
		within(packageSection).getByRole("button", {
			name: /Подготовить и проверить выбранную версию/,
		}),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Подтвердить проверку версии" }),
	);
}

it("requires path, explicit verified tools, empty output check and MusicBrainz connection; genres stay optional", () => {
	render(<StatefulSetup />);
	const next = screen.getByRole("button", { name: "Продолжить" });
	expect(next).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Проверить путь" }));
	expect(next).toBeEnabled();
	fireEvent.click(next);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	chooseToolRelease("ffmpeg", "8.0.1");
	expect(
		screen.getByText(/проверяемые executable: ffmpeg, ffprobe/i),
	).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	chooseToolRelease("fpcalc", "1.5.1");
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	expect(
		screen.getByRole("heading", { name: "Публикация" }),
	).toBeInTheDocument();
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
	const picker = screen.getByRole("dialog", { name: "Папка на сервере" });
	const server = within(picker).queryByRole("button", { name: "Сервер" });
	if (server) fireEvent.click(server);
	fireEvent.click(screen.getByRole("button", { name: "srv ›" }));
	fireEvent.click(screen.getByRole("button", { name: "music ›" }));
	fireEvent.click(
		within(picker).getByRole("button", { name: "Выбрать папку" }),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить путь и пустоту · демо" }),
	);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	fireEvent.click(
		screen.getByRole("button", {
			name: /Источник MusicBrainz · выбор обязателен/,
		}),
	);
	fireEvent.click(
		screen.getByRole("option", { name: "Публичный MusicBrainz" }),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить соединение MusicBrainz" }),
	);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled();
	expect(
		screen.getByText(/Библиотека жанров · необязательно/),
	).toBeInTheDocument();
	// No genre fetch is required to pass setup.
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	expect(screen.getByRole("heading", { name: "Итог" })).toBeInTheDocument();
});

it("keeps a failed tool inactive and allows an explicit retry; MusicBrainz errors are retryable", () => {
	render(<StatefulSetup />);
	fireEvent.click(screen.getByRole("button", { name: "Проверить путь" }));
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	fireEvent.click(
		screen.getByLabelText("Демо: имитировать ошибку загрузки/проверки"),
	);
	fireEvent.click(
		screen.getByRole("button", { name: /Выбрать release · ffmpeg/ }),
	);
	fireEvent.click(screen.getByRole("option", { name: "8.0.1 · demo release" }));
	fireEvent.click(
		within(
			screen
				.getByRole("heading", { name: "FFmpeg package · ffmpeg + ffprobe" })
				.closest("section")!,
		).getByRole("button", {
			name: /Подготовить и проверить выбранную версию/,
		}),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Подтвердить проверку версии" }),
	);
	expect(screen.getByRole("alert")).toHaveTextContent(
		"активная версия не изменена",
	);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	fireEvent.click(
		screen.getByLabelText("Демо: имитировать ошибку загрузки/проверки"),
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: "Повторить проверку выбранной версии",
		}),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Подтвердить проверку версии" }),
	);
	expect(screen.getByText(/проверен и активен · 8.0.1/)).toBeInTheDocument();
	// A retryable connection failure is made available on the mandatory step.
	chooseToolRelease("fpcalc", "1.5.1");
	fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
	fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
	const picker = screen.getByRole("dialog", { name: "Папка на сервере" });
	const server = within(picker).queryByRole("button", { name: "Сервер" });
	if (server) fireEvent.click(server);
	fireEvent.click(screen.getByRole("button", { name: "srv ›" }));
	fireEvent.click(screen.getByRole("button", { name: "music ›" }));
	fireEvent.click(
		within(picker).getByRole("button", { name: "Выбрать папку" }),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить путь и пустоту · демо" }),
	);
	fireEvent.click(
		screen.getByRole("button", {
			name: /Источник MusicBrainz · выбор обязателен/,
		}),
	);
	fireEvent.click(
		screen.getByRole("option", { name: "Публичный MusicBrainz" }),
	);
	fireEvent.click(screen.getByLabelText("Демо: имитировать ошибку соединения"));
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить соединение MusicBrainz" }),
	);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
	expect(
		screen.getByText(/проверка MusicBrainz не удалась/),
	).toBeInTheDocument();
	fireEvent.click(screen.getByLabelText("Демо: имитировать ошибку соединения"));
	fireEvent.click(
		screen.getByRole("button", { name: "Проверить соединение MusicBrainz" }),
	);
	expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled();
});

it("rechecks setup requirements before saving the explicitly chosen output path", () => {
	function ReadySetup() {
		const [settings, setSettings] = useState<DemoSettings>({
			...initialDemoSettings,
			currentOutput: "/srv/music",
			pathChecked: true,
			selectedRelease: { ffmpeg: "8.0.1", fpcalc: "1.5.1" },
			setupVerified: { ffmpeg: "8.0.1", fpcalc: "1.5.1" },
			installations: [
				{
					id: "ffmpeg-8.0.1",
					package: "ffmpeg" as const,
					release: "8.0.1",
					status: "active" as const,
					occupied: false,
				},
				...initialDemoSettings.installations!.filter(
					(item) => item.package === "fpcalc",
				),
			],
			outputCandidate: "/srv/new-library",
			outputChecked: true,
			musicBrainzMode: "public" as const,
			musicBrainzVerified: true,
		});
		return (
			<>
				<SetupScreen settings={settings} onSettings={setSettings} step={3} />
				<output>{settings.currentOutput}</output>
			</>
		);
	}

	render(<ReadySetup />);
	fireEvent.click(
		screen.getByRole("button", { name: "Завершить демонстрацию" }),
	);
	expect(screen.getByText("/srv/new-library")).toBeInTheDocument();
});
