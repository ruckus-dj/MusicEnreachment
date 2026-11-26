import type { ReactElement } from "react";
import { Heading, Input, Label, TextField } from "react-aria-components";
import { ActionDialog, ActionModal } from "../../components/ActionModal";
import { AppButton as Button } from "../../components/AppButton";
import {
  RowActionMenu,
  type RowActionMenuItem,
} from "../../components/RowActionMenu";
import type { Locale } from "../../i18n/types";
import { operationMessage } from "../operations/operationMessages";

const objectActions = "my-2 flex flex-wrap items-center gap-2";

export type TrackActionsViewProps = {
  locale: Locale;
  notice: string;
  rename: {
    open: boolean;
    title: string;
    onOpenChange: (open: boolean) => void;
    onTitleChange: (title: string) => void;
    onStart: () => void;
    onSubmit: () => void;
  };
  onRefresh: () => void;
  onRepublish?: () => void;
  deletion?: ReactElement;
};

export function TrackActionsView({
  locale,
  notice,
  rename,
  onRefresh,
  onRepublish,
  deletion,
}: TrackActionsViewProps) {
  const m = (key: Parameters<typeof operationMessage>[1]) =>
    operationMessage(locale, key);
  const secondaryActions: RowActionMenuItem[] = [
    {
      id: "refresh-provider-cache",
      label: m("refreshProviderCache"),
      onAction: () => onRefresh(),
    },
  ];
  if (onRepublish) {
    secondaryActions.push({
      id: "republish",
      label: m("republishExplicit"),
      onAction: () => onRepublish(),
    });
  }
  return (
    <>
      <div className={objectActions}>
        <Button onPress={rename.onStart}>{m("editLocalTitle")}</Button>
        <RowActionMenu
          label={m("otherTrackActions")}
          size="default"
          actions={secondaryActions}
        />
      </div>
      {deletion}
      {notice && <p role="status">{notice}</p>}
      <ActionModal
        isOpen={rename.open}
        onOpenChange={rename.onOpenChange}
        isDismissable
      >
        <ActionDialog
          aria-labelledby="track-action-title"
          className="grid gap-2.5"
        >
          <form
            onSubmit={(event) => {
              event.preventDefault();
              rename.onSubmit();
            }}
          >
            <Heading slot="title" id="track-action-title">
              {m("editTagsOnly")}
            </Heading>
            <TextField
              aria-label={m("trackTitle")}
              value={rename.title}
              onChange={rename.onTitleChange}
              isRequired
            >
              <Label className="mb-1 block text-xs font-semibold text-[var(--muted)]">
                {m("trackTitle")}
              </Label>
              <Input
                autoComplete="off"
                className="min-h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 py-[5px] text-[var(--ink)]"
              />
            </TextField>
            <p>{m("renamePreserves")}</p>
            <div className={objectActions}>
              <Button type="button" onPress={() => rename.onOpenChange(false)}>
                {m("cancelAction")}
              </Button>
              <Button type="submit">{m("confirmAction")}</Button>
            </div>
          </form>
        </ActionDialog>
      </ActionModal>
    </>
  );
}
