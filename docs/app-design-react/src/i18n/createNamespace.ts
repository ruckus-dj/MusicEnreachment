import type { NamespaceCatalog, PlaceholderNames } from "./types";

type PlaceholderParity<
	English extends Record<string, string>,
	Russian extends { [Key in keyof English]: string },
> = {
	[Key in keyof English]: [PlaceholderNames<English[Key]>] extends [
		PlaceholderNames<Russian[Key]>,
	]
		? [PlaceholderNames<Russian[Key]>] extends [PlaceholderNames<English[Key]>]
			? unknown
			: never
		: never;
};

type ExactLocaleKeys<
	English extends Record<string, string>,
	Russian extends { [Key in keyof English]: string },
> = Exclude<keyof Russian, keyof English> extends never ? unknown : never;

/** Define an English/Russian namespace while checking that both sides share keys. */
export function createNamespace<
	const English extends Record<string, string>,
	const Russian extends { [Key in keyof English]: string },
>(catalog: {
	en: English;
	ru: Russian &
		PlaceholderParity<English, Russian> &
		ExactLocaleKeys<English, Russian>;
}): NamespaceCatalog<English, Russian> {
	return catalog;
}
