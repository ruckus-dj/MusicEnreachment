import { useRef, useState } from "react";
import type { LocalProviderItem } from "../../api/generated/client.schemas";
import { OwnCatalogRecordEditorView } from "../../ui/features/local-catalog/OwnCatalogRecordEditorView";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "../../ui/features/local-catalog/ownCatalogMessages";
import { LocalRecordingArtistField } from "../../ui/features/local-release/LocalRecordingArtistField";
import {
  type FindLocalArtists,
  formatLocalDuration,
  isOutcomeUncertain,
  type LocalRecordingCreditSelection,
  type LocalReleaseArtist,
  parseLocalDuration,
  type SaveLocalArtist,
} from "../../ui/features/local-release/localReleaseFormModel";
import { localReleaseMessages } from "../../ui/features/local-release/localReleaseMessages";
import type { Locale } from "../../ui/i18n/locale";
import type { GenreReference } from "../library/GenreMetadataEditor";
import { ProviderCatalogGenreField } from "../local-release/ProviderCatalogGenreField";
import {
  type ArtistFormDraft,
  type RecordingFormDraft,
  recordingCreditsFromItem,
} from "./ownCatalogModel";

function genreReferences(item: LocalProviderItem | undefined) {
  return (item?.genres ?? []).map((genre) => ({
    id: genre.id,
    name: genre.name,
    source_name: genre.source_name,
  }));
}

export function OwnCatalogArtistForm({
  locale,
  record,
  onSave,
  onBack,
}: {
  locale: Locale;
  record?: LocalProviderItem;
  onSave: (draft: ArtistFormDraft) => Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const [name, setName] = useState(record?.name ?? "");
  const [sortName, setSortName] = useState(record?.sort_name ?? "");
  const [tags, setTags] = useState((record?.tags ?? []).join(", "));
  const [genres, setGenres] = useState<GenreReference[]>(() =>
    genreReferences(record),
  );
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const savingRef = useRef(false);

  async function save() {
    if (savingRef.current || uncertain || !name.trim()) return;
    savingRef.current = true;
    setSaving(true);
    setSaveError(false);
    try {
      await onSave({ name, sortName, tags, genres });
    } catch (error) {
      if (isOutcomeUncertain(error)) setUncertain(true);
      else setSaveError(true);
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  return (
    <OwnCatalogRecordEditorView
      notice={m("formRequiredNotice")}
      fields={[
        {
          label: `${m("name")} · ${m("required")}`,
          value: name,
          onChange: setName,
          required: true,
          disabled: saving,
        },
        {
          label: `${m("sortName")} · ${m("optional")}`,
          value: sortName,
          onChange: setSortName,
          disabled: saving,
        },
        {
          label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
          value: tags,
          onChange: setTags,
          disabled: saving,
        },
      ]}
      genre={
        <ProviderCatalogGenreField
          locale={locale}
          owner="artist"
          value={genres}
          onChange={setGenres}
          disabled={saving}
        />
      }
      error={
        uncertain ? m("saveUncertain") : saveError ? m("saveError") : undefined
      }
      save={{
        label: m("save"),
        disabled: !name.trim() || saving || uncertain,
        onPress: () => void save(),
      }}
      cancel={{ label: m("cancel"), disabled: saving, onPress: onBack }}
    />
  );
}

export function OwnCatalogRecordingForm({
  locale,
  record,
  findArtists,
  saveArtist,
  onSave,
  onBack,
}: {
  locale: Locale;
  record?: LocalProviderItem;
  findArtists: FindLocalArtists;
  saveArtist: SaveLocalArtist;
  onSave: (draft: RecordingFormDraft) => Promise<void>;
  onBack: () => void;
}) {
  const m = (key: OwnCatalogMessage) => ownCatalogMessage(locale, key);
  const copy = localReleaseMessages[locale];
  const [title, setTitle] = useState(record?.title ?? "");
  const [duration, setDuration] = useState(
    formatLocalDuration(record?.duration_ms),
  );
  const [tags, setTags] = useState((record?.tags ?? []).join(", "));
  const [genres, setGenres] = useState<GenreReference[]>(() =>
    genreReferences(record),
  );
  const [credits, setCredits] = useState<LocalRecordingCreditSelection[]>(() =>
    record ? recordingCreditsFromItem(record) : [],
  );
  const [savedArtists, setSavedArtists] = useState<LocalReleaseArtist[]>([]);
  const [badDuration, setBadDuration] = useState(false);
  const [artistSaving, setArtistSaving] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const savingRef = useRef(false);

  async function save() {
    if (savingRef.current || uncertain || artistSaving || !title.trim()) return;
    if (Number.isNaN(parseLocalDuration(duration))) {
      setBadDuration(true);
      return;
    }
    setBadDuration(false);
    savingRef.current = true;
    setSaving(true);
    setSaveError(false);
    try {
      await onSave({ title, durationInput: duration, tags, genres, credits });
    } catch (error) {
      if (isOutcomeUncertain(error)) setUncertain(true);
      else setSaveError(true);
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  }

  return (
    <OwnCatalogRecordEditorView
      notice={m("formRequiredNotice")}
      help={m("durationHelp")}
      fields={[
        {
          label: `${m("titleField")} · ${m("required")}`,
          value: title,
          onChange: setTitle,
          required: true,
          disabled: saving,
        },
        {
          label: `${m("durationField")} · ${m("optional")}`,
          value: duration,
          onChange: (value) => {
            setDuration(value);
            setBadDuration(false);
          },
          placeholder: "3:24",
          disabled: saving,
        },
        {
          label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
          value: tags,
          onChange: setTags,
          disabled: saving,
        },
      ]}
      fieldError={badDuration ? m("badDuration") : undefined}
      genre={
        <ProviderCatalogGenreField
          locale={locale}
          owner="recording"
          value={genres}
          onChange={setGenres}
          disabled={saving}
        />
      }
      extra={
        <LocalRecordingArtistField
          copy={copy}
          findArtists={findArtists}
          saveArtist={saveArtist}
          credits={credits}
          saved={savedArtists}
          busy={saving}
          onCreditsChange={setCredits}
          onSavingChange={setArtistSaving}
          onSaved={(artist) =>
            setSavedArtists((current) =>
              current.some((item) => item.id === artist.id)
                ? current
                : [...current, artist],
            )
          }
        />
      }
      error={
        uncertain ? m("saveUncertain") : saveError ? m("saveError") : undefined
      }
      save={{
        label: m("save"),
        disabled: !title.trim() || saving || artistSaving || uncertain,
        onPress: () => void save(),
      }}
      cancel={{ label: m("cancel"), disabled: saving, onPress: onBack }}
    />
  );
}
