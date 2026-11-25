import { createSourcesRouteOutlet } from "../../ui/features/sources/SourcesRouteOutlet";
import type { SourceLocationsProps } from "./SourceLocations";

// One module-scope pair keeps the typed inventory controller mounted while the
// list, root, inspector, and missing-root pages share the same outlet.
export const { SourcesBinder, SourcesOutlet } =
  createSourcesRouteOutlet<SourceLocationsProps>();
