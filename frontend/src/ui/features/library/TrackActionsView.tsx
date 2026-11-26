import type { ReactElement } from "react";
import {
  Disclosure,
  DisclosureGroup,
  DisclosurePanel,
  Heading,
  Input,
  Label,
  TextField,
} from "react-aria-components";
import { ActionDialog, ActionModal } from "../../components/ActionModal";
import { AppButton as Button } from "../../components/AppButton";
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
  return (
    <>
      <DisclosureGroup>
        <Disclosure
          id="track-actions"
          className="border-b border-[var(--line)] py-[7px] [&_[slot=trigger]]:border-0 [&_[slot=trigger]]:bg-transparent [&_[slot=trigger]]:py-1 [&_[slot=trigger]]:text-[var(--accent)] [&_p]:my-1.5 [&_p]:text-[var(--muted)]"
        >
          <Button slot="trigger">{m("otherTrackActions")}</Button>
          <DisclosurePanel className="min-w-0">
            <div className={objectActions}>
              <Button onPress={rename.onStart}>{m("editLocalTitle")}</Button>
              <Button onPress={onRefresh}>{m("refreshProviderCache")}</Button>
              {onRepublish && (
                <Button onPress={onRepublish}>{m("republishExplicit")}</Button>
              )}
            </div>
            {deletion}
          </DisclosurePanel>
        </Disclosure>
      </DisclosureGroup>
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
              <Label className="mb-1 block text-[11px] font-semibold text-[var(--muted)]">
                {m("trackTitle")}
              </Label>
              <Input
                autoComplete="off"
                className="min-h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] text-[var(--ink)]"
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
