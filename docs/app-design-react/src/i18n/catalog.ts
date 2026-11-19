import { catalogMessages } from "../catalogMessages";
import { catalogueMessages } from "../catalogueMessages";
import { operationMessages } from "../features/operations/operationMessages";
import { genreMessages } from "../genres/genreMessages";
import { incomingMessages } from "../incomingMessages";
import { libraryRowsMessages } from "../features/library/libraryRowsMessages";
import { localProviderMessages } from "../localProviderMessages";
import { matchingMessages } from "../matchingMessages";
import { matchingSearchPickerMessages } from "../matchingSearchPickerMessages";
import { metadataEditorMessages } from "../metadata/editorMessages";
import { metadataMessages } from "../metadata/messages";
import { publicationMessages } from "../publicationMessages";
import { sourcesMessages } from "../sourceMessages";
import { trackMessages } from "../trackMessages";
import { common } from "./namespaces/common";
import { settingsMessages } from "./namespaces/settings";
import { shell } from "./namespaces/shell";
import type { I18nCatalog } from "./types";

export { createNamespace } from "./createNamespace";
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
