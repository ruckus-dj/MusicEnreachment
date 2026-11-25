import { incomingMessages } from "../../ui/features/incoming/incomingOverviewMessages";
import { catalogMessages } from "../../ui/features/library/catalogMessages";
import { catalogueMessages } from "../../ui/features/library/catalogueMessages";
import { genreMessages } from "../../ui/features/library/genreCatalogMessages";
import { libraryRowsMessages } from "../../ui/features/library/libraryRowsMessages";
import { metadataEditorMessages } from "../../ui/features/library/metadata/metadataEditorMessages";
import { metadataMessages } from "../../ui/features/library/metadata/metadataFieldMessages";
import { trackMessages } from "../../ui/features/library/trackMessages";
import { localProviderMessages } from "../../ui/features/local-catalog/localProviderMessages";
import { matchingSearchPickerMessages } from "../../ui/features/matching/matchingSearchPickerMessages";
import { matchingMessages } from "../../ui/features/matching/matchingWorkspaceMessages";
import { operationMessages } from "../../ui/features/operations/operationMessages";
import { publicationMessages } from "../../ui/features/publication/publicationMessages";
import { settingsMessages } from "../../ui/features/settings/settingsFormMessages";
import { sourcesMessages } from "../../ui/features/sources/sourceInventoryMessages";
import { common } from "../../ui/i18n/namespaces/common";
import { shell } from "../../ui/i18n/namespaces/shell";
import type { I18nCatalog } from "../../ui/i18n/types";

export { createNamespace } from "../../ui/i18n/createNamespace";
export { common } from "../../ui/i18n/namespaces/common";

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
