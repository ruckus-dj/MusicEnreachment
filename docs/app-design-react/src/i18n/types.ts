export const supportedLocales = ["en", "ru"] as const;

export type Locale = (typeof supportedLocales)[number];
export type LanguagePreference = "system" | Locale;

export type NamespaceCatalog<
	English extends Record<string, string>,
	Russian extends { [Key in keyof English]: string } = {
		[Key in keyof English]: string;
	},
> = {
	en: English;
	ru: Russian;
};

export type I18nCatalog = Record<
	string,
	NamespaceCatalog<Record<string, string>>
>;

export type TranslationKey<Catalog extends I18nCatalog> = {
	[Namespace in keyof Catalog &
		string]: `${Namespace}.${keyof Catalog[Namespace]["en"] & string}`;
}[keyof Catalog & string];

export type PlaceholderNames<Message extends string> =
	Message extends `${string}{${infer Name}}${infer Rest}`
		? Name | PlaceholderNames<Rest>
		: never;

type MessageForKey<
	Catalog extends I18nCatalog,
	Key extends TranslationKey<Catalog>,
> = Key extends `${infer Namespace}.${infer Message}`
	? Message extends keyof Catalog[Namespace & keyof Catalog]["en"]
		? Catalog[Namespace & keyof Catalog]["en"][Message] & string
		: never
	: never;

export type TranslationParams<
	Catalog extends I18nCatalog,
	Key extends TranslationKey<Catalog>,
> =
	PlaceholderNames<MessageForKey<Catalog, Key>> extends never
		? []
		: [
				values: Record<
					PlaceholderNames<MessageForKey<Catalog, Key>>,
					string | number
				>,
			];

export type Translator<Catalog extends I18nCatalog> = <
	Key extends TranslationKey<Catalog>,
>(
	key: Key,
	...params: TranslationParams<Catalog, Key>
) => string;
