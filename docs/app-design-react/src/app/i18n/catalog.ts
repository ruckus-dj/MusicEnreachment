import { genreMessages } from "../../demo/providers/genres/genreMessages";
import { incomingMessages } from "../../demo/providers/incoming/incomingMessages";
import { catalogMessages } from "../../demo/providers/library/catalogMessages";
import { libraryRowsMessages } from "../../demo/providers/library/libraryRowsMessages";
import { metadataEditorMessages } from "../../demo/providers/library/metadata/editorMessages";
import { metadataMessages } from "../../demo/providers/library/metadata/messages";
import { trackMessages } from "../../demo/providers/library/trackMessages";
import { localProviderMessages } from "../../demo/providers/local-catalog/localProviderMessages";
import { matchingMessages } from "../../demo/providers/matching/matchingMessages";
import { matchingSearchPickerMessages } from "../../demo/providers/matching/matchingSearchPickerMessages";
import { operationMessages } from "../../demo/providers/operations/operationMessages";
import { publicationMessages } from "../../demo/providers/publication/publicationMessages";
import { sourcesMessages } from "../../demo/providers/sources/sourceMessages";
import { catalogueMessages } from "../../ui/features/library/catalogueMessages";
import { shell } from "../../ui/i18n/namespaces/shell";
import type { I18nCatalog } from "../../ui/i18n/types";
import { common } from "./namespaces/common";
import { settingsMessages } from "./namespaces/settings";

export { createNamespace } from "../../ui/i18n/createNamespace";
export { common } from "./namespaces/common";

/** Add new typed namespaces here; use createNamespace for each locale pair. */
export const catalog = {
	common,
	shell,
	settings: settingsMessages,
	matching: matchingMessages,
	track: trackMessages,
	publication: publicationMessages,
	operations: operationMessages,
	sources: sourcesMessages,
	genres: genreMessages,
	catalogue: catalogueMessages,
	library: catalogMessages,
	libraryRows: libraryRowsMessages,
	incoming: incomingMessages,
	metadata: metadataMessages,
	localProvider: localProviderMessages,
	matchingSearch: matchingSearchPickerMessages,
	metadataEditors: metadataEditorMessages,
} satisfies I18nCatalog;
