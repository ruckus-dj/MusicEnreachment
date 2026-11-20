import { useI18n } from "../../i18n/I18nProvider";
import type {
  PublicationPreviewState,
  SourceChoice,
} from "./FirstPublicationPreview";
import { PublicationPreviewDetailsView } from "./PublicationPreviewDetailsView";

/**
 * Application adapter for the portable publication details view. It supplies
 * the active locale and forwards the controller snapshot unchanged.
 */
export function PublicationPreviewDetails({
  state,
  sourceGroupName,
  onSourceChoice,
  onRefresh,
}: {
  state: PublicationPreviewState;
  sourceGroupName: string;
  onSourceChoice: (choice: SourceChoice | null) => void;
  onRefresh: () => void;
}) {
  const { locale } = useI18n();
  return (
    <PublicationPreviewDetailsView
      locale={locale}
      state={state}
      sourceGroupName={sourceGroupName}
      onSourceChoice={onSourceChoice}
      onRefresh={onRefresh}
    />
  );
}
