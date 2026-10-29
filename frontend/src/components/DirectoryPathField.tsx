import { useId, useState } from "react";
import { DirectoryPicker } from "./DirectoryPicker";
import "./DirectoryPathField.css";

export function DirectoryPathField({
  id,
  label,
  value,
  onSelect,
  disabled = false,
  description,
  "aria-describedby": ariaDescribedBy,
}: {
  id: string;
  label: string;
  value: string;
  onSelect: (path: string) => void;
  disabled?: boolean;
  description?: string;
  "aria-describedby"?: string;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const descriptionId = useId();
  const helpIds = [ariaDescribedBy, description ? descriptionId : undefined]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="directory-path-field">
      <label htmlFor={id}>{label}</label>
      <div className="directory-path-field__control">
        <input
          id={id}
          type="text"
          readOnly
          value={value}
          disabled={disabled}
          aria-describedby={helpIds || undefined}
        />
        <button
          type="button"
          disabled={disabled}
          onClick={() => setPickerOpen(true)}
        >
          Выбрать папку
        </button>
      </div>
      {description && <p id={descriptionId}>{description}</p>}
      <DirectoryPicker
        open={pickerOpen}
        initialPath={value}
        onSelect={onSelect}
        onClose={() => setPickerOpen(false)}
      />
    </div>
  );
}
