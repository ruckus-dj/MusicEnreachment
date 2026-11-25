import type { Ref } from "react";
import { AppButton } from "../../components/AppButton";

const copy = {
  en: { edit: "Edit", save: "Save", saving: "Saving…", cancel: "Cancel" },
  ru: {
    edit: "Изменить",
    save: "Сохранить",
    saving: "Сохранение…",
    cancel: "Отмена",
  },
} as const;

export function MetadataCardActions({
  locale,
  title,
  editing,
  saving,
  editOrSaveDisabled,
  hidden,
  actionsRef,
  onEditOrSave,
  onCancel,
}: {
  locale: "en" | "ru";
  title: string;
  editing: boolean;
  saving: boolean;
  editOrSaveDisabled?: boolean;
  hidden?: boolean;
  actionsRef?: Ref<HTMLDivElement>;
  onEditOrSave: () => void;
  onCancel: () => void;
}) {
  const text = copy[locale];
  return (
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
      <h2 className="m-0 text-sm font-semibold">{title}</h2>
      <div
        ref={actionsRef}
        aria-hidden={hidden || undefined}
        className={`grid grid-cols-[7.5rem_7.5rem] gap-2 ${hidden ? "invisible" : ""}`}
      >
        <AppButton
          type="button"
          isDisabled={hidden || saving || editOrSaveDisabled}
          onPress={onEditOrSave}
          className="min-w-0 justify-center"
        >
          {saving ? text.saving : editing ? text.save : text.edit}
        </AppButton>
        <AppButton
          type="button"
          isDisabled={hidden || !editing || saving}
          aria-hidden={hidden || !editing}
          excludeFromTabOrder={hidden || !editing}
          onPress={onCancel}
          className={`min-w-0 justify-center ${editing ? "" : "invisible"}`}
        >
          {text.cancel}
        </AppButton>
      </div>
    </div>
  );
}
