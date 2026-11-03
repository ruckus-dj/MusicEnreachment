import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { LocalProviderScreen } from "./LocalProviderScreen";
import {
	publicationMetadataIssueMessages,
	originalReleases,
} from "./catalogData";
import { I18nProvider, useI18n } from "./i18n";
import { createLocalProviderState } from "./localProviderModel";
import {
	metadataValidationMessage,
	validateMetadataIssues,
} from "./metadata/projection";

const languageStorageKey = "music-design-v2-language";

afterEach(() => {
	localStorage.removeItem(languageStorageKey);
});

it("formats publication validation issues for the requested locale", () => {
	const release = structuredClone(originalReleases[0]);
	const track = release.tracks[0];
	release.metadataValues = { releasedate: ["not-a-date"] };

	const russian = publicationMetadataIssueMessages(release, track, "ru");
	const english = publicationMetadataIssueMessages(release, track, "en");

	expect(russian).toHaveLength(1);
	expect(english).toHaveLength(1);
	expect(russian[0]).not.toBe(english[0]);
});

it("reformats a local-provider validation error when the locale changes", () => {
	localStorage.setItem(languageStorageKey, "ru");
	const state = createLocalProviderState();
	state.type = "releases";
	state.selectedId = "local-release-north";
	const selected = state.items.releases.find(
		(release) => release.id === state.selectedId,
	);
	expect(selected).toBeDefined();
	selected!.metadataValues = { releasedate: ["not-a-date"] };
	const issues = validateMetadataIssues(selected!.metadataValues, "release");

	function LanguageButtons() {
		const { setPreference } = useI18n();
		return (
			<>
				<button onClick={() => setPreference("ru")}>Русский</button>
				<button onClick={() => setPreference("en")}>English</button>
			</>
		);
	}

	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<I18nProvider>
				<LanguageButtons />
				<LocalProviderScreen state={current} onState={setCurrent} />
			</I18nProvider>
		);
	}

	render(<Harness />);
	fireEvent.click(screen.getAllByRole("button", { name: "Редактировать" })[0]);
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	const alert = screen.getByRole("alert");
	expect(alert).toHaveTextContent(metadataValidationMessage(issues[0], "ru"));

	fireEvent.click(screen.getByRole("button", { name: "English" }));
	expect(alert).toHaveTextContent(metadataValidationMessage(issues[0], "en"));
	expect(alert).not.toHaveTextContent(
		metadataValidationMessage(issues[0], "ru"),
	);
});
