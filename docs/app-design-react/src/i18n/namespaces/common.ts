import { createNamespace } from "../createNamespace";

export const common = createNamespace({
	en: {
		language: "Language",
		languageSystem: "System default",
		languageEnglish: "English",
		languageRussian: "Russian",
		preferenceReadWarning:
			"Language preference is active but could not be read from browser storage.",
		preferenceWriteWarning:
			"Language preference is active but could not be saved in browser storage.",
	},
	ru: {
		language: "Язык",
		languageSystem: "Системный",
		languageEnglish: "Английский",
		languageRussian: "Русский",
		preferenceReadWarning:
			"Настройка языка действует, но не удалось прочитать её из хранилища браузера.",
		preferenceWriteWarning:
			"Настройка языка действует, но не удалось сохранить её в хранилище браузера.",
	},
});
