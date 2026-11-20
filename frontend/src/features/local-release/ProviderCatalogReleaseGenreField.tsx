import type { LocalReleaseGenreFieldProps } from "./LocalReleaseForm";
import { ProviderCatalogGenreField } from "./ProviderCatalogGenreField";

/** Production adapter for the Local release form's genre slot. */
export function ProviderCatalogReleaseGenreField(
  props: LocalReleaseGenreFieldProps,
) {
  return <ProviderCatalogGenreField owner="release" {...props} />;
}
