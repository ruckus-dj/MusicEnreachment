import { incomingMessages } from "../features/incoming/incomingOverviewMessages";
import { catalogMessages } from "../features/library/catalogMessages";
import { catalogueMessages } from "../features/library/catalogueMessages";
import { libraryRowsMessages } from "../features/library/libraryRowsMessages";
import { metadataEditorMessages } from "../features/library/metadata/metadataEditorMessages";
import { metadataMessages } from "../features/library/metadata/metadataFieldMessages";
import { trackMessages } from "../features/library/trackMessages";
import { matchingSearchPickerMessages } from "../features/matching/matchingSearchPickerMessages";
import { matchingMessages } from "../features/matching/matchingWorkspaceMessages";
import { operationMessages } from "../features/operations/operationMessages";
import { publicationMessages } from "../features/publication/publicationMessages";
import { settingsMessages } from "../features/settings/settingsFormMessages";
import { common } from "./namespaces/common";
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
  catalogue: catalogueMessages,
  library: catalogMessages,
  libraryRows: libraryRowsMessages,
  incoming: incomingMessages,
  metadata: metadataMessages,
  matchingSearch: matchingSearchPickerMessages,
  metadataEditors: metadataEditorMessages,
} satisfies I18nCatalog;
