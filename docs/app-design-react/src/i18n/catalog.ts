import type { I18nCatalog } from "./types";
import { common } from "./namespaces/common";
import { localProviderMessages } from "../localProviderMessages";
import { shell } from "./namespaces/shell";
import { settingsMessages } from "./namespaces/settings";
import { matchingMessages } from "../matchingMessages";
import { trackMessages } from "../trackMessages";
import { publicationMessages } from "../publicationMessages";
import { operationMessages } from "../operationMessages";
import { sourcesMessages } from "../sourceMessages";
import { genreMessages } from "../genres/genreMessages";
import { catalogueMessages } from "../catalogueMessages";
import { catalogMessages } from "../catalogMessages";
import { incomingMessages } from "../incomingMessages";
import { metadataMessages } from "../metadata/messages";
import { libraryRowsMessages } from "../libraryRowsMessages";
import { matchingSearchPickerMessages } from "../matchingSearchPickerMessages";
import { metadataEditorMessages } from "../metadata/editorMessages";

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
