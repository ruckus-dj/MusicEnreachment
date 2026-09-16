export const ENCODING_CODECS = [
  "utf-8",
  "utf-16",
  "utf-16-le",
  "utf-16-be",
  "latin-1",
  "cp1251",
  "cp1252",
  "koi8-r",
  "cp866",
] as const;
export type EncodingCodec = (typeof ENCODING_CODECS)[number];
export type EncodingChoice = { readonly field_id: number } & (
  | {
      readonly mode: "keep" | "original";
      readonly encode_codec?: null;
      readonly decode_codec?: null;
    }
  | {
      readonly mode: "decode" | "codec";
      readonly encode_codec?: null;
      readonly decode_codec: EncodingCodec;
    }
  | {
      readonly mode: "unicode";
      readonly encode_codec: EncodingCodec;
      readonly decode_codec: EncodingCodec;
    }
);
export type EncodingField = {
  readonly field_id: number;
  readonly tag_name: string;
  readonly container: string;
  readonly physical_id: string | null;
  readonly extraction_version: string | null;
  readonly selected: boolean;
  readonly original_value: string;
  readonly current_value: string;
  readonly raw_evidence_available: boolean;
  readonly declared_codec: string | null;
  readonly applied_choice: EncodingChoice | null;
  readonly decision_origin?: "manual" | "auto" | null;
};
export type EncodingDetail = {
  readonly source_id: string;
  readonly source_revision: number;
  readonly fields: readonly EncodingField[];
  readonly suggestions?: readonly {
    readonly field_id: number;
    readonly state: "suggested" | "review";
    readonly value: string | null;
    readonly choice: EncodingChoice | null;
    readonly reason: string;
  }[];
};
export type EncodingRequest = {
  readonly expected_revision: number;
  readonly choices: readonly EncodingChoice[];
};
export type EncodingPreview = {
  readonly source_id: string;
  readonly source_revision: number;
  readonly valid: boolean;
  readonly fields: readonly {
    readonly field_id: number;
    readonly value: string | null;
    readonly status: "unchanged" | "changed" | "error";
    readonly error: string | null;
  }[];
};
export type EncodingApplied = EncodingPreview & { readonly queued: boolean };

export function isEncodingPreview(value: unknown): value is EncodingPreview {
  if (typeof value !== "object" || value === null) return false;
  return (
    "source_id" in value &&
    typeof value.source_id === "string" &&
    "source_revision" in value &&
    typeof value.source_revision === "number" &&
    "valid" in value &&
    typeof value.valid === "boolean" &&
    "fields" in value &&
    Array.isArray(value.fields) &&
    value.fields.every(
      (field: unknown) =>
        typeof field === "object" &&
        field !== null &&
        "field_id" in field &&
        typeof field.field_id === "number" &&
        "value" in field &&
        (field.value === null || typeof field.value === "string") &&
        "status" in field &&
        ["unchanged", "changed", "error"].includes(String(field.status)) &&
        "error" in field &&
        (field.error === null || typeof field.error === "string"),
    )
  );
}
