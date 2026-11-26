import {
  MetadataEditorView,
  type MetadataEditorViewProps,
} from "../MetadataEditorView";
import {
  type MetadataProviderLink,
  MetadataProviderLinks,
} from "./MetadataProviderLinks";

export type ReleaseMetadataEditorViewProps = {
  editor: MetadataEditorViewProps;
  providerLinks?: readonly MetadataProviderLink[];
};

export function ReleaseMetadataEditorView({
  editor,
  providerLinks,
}: ReleaseMetadataEditorViewProps) {
  return (
    <div className="grid min-w-0 gap-3">
      <MetadataEditorView {...editor} />
      <MetadataProviderLinks locale={editor.locale} links={providerLinks} />
    </div>
  );
}
