import { useRef, useState } from "react";
import { Button } from "react-aria-components";
import type { LocalProviderItem } from "../../api/generated/client.schemas";
import { OwnCatalogRecordFieldsView } from "../../ui/features/local-catalog/OwnCatalogRecordFieldsView";
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
import {
  actions,
  errorClass,
  panel,
  primary,
  secondary,
} from "../../ui/features/local-release/localReleaseUi";
import type { Locale } from "../../ui/i18n/locale";
import type { GenreReference } from "../library/GenreMetadataEditor";
import { ProviderCatalogGenreField } from "../local-release/ProviderCatalogGenreField";
import {
  type OwnCatalogMessage,
  ownCatalogMessage,
} from "./ownCatalogMessages";
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
    <section className={`${panel} grid gap-3`}>
      <OwnCatalogRecordFieldsView
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
      />
      <ProviderCatalogGenreField
        locale={locale}
        owner="artist"
        value={genres}
        onChange={setGenres}
        disabled={saving}
      />
      {(saveError || uncertain) && (
        <p className={errorClass} role="alert">
          {uncertain ? m("saveUncertain") : m("saveError")}
        </p>
      )}
      <div className={actions}>
        <Button
          className={primary}
          isDisabled={!name.trim() || saving || uncertain}
          onPress={() => void save()}
        >
          {m("save")}
        </Button>
        <Button className={secondary} isDisabled={saving} onPress={onBack}>
          {m("cancel")}
        </Button>
      </div>
    </section>
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
    <section className={`${panel} grid gap-3`}>
      <OwnCatalogRecordFieldsView
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
      />
      {badDuration && (
        <p className={errorClass} role="alert">
          {m("badDuration")}
        </p>
      )}
      <ProviderCatalogGenreField
        locale={locale}
        owner="recording"
        value={genres}
        onChange={setGenres}
        disabled={saving}
      />
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
      {(saveError || uncertain) && (
        <p className={errorClass} role="alert">
          {uncertain ? m("saveUncertain") : m("saveError")}
        </p>
      )}
      <div className={actions}>
        <Button
          className={primary}
          isDisabled={!title.trim() || saving || artistSaving || uncertain}
          onPress={() => void save()}
        >
          {m("save")}
        </Button>
        <Button className={secondary} isDisabled={saving} onPress={onBack}>
          {m("cancel")}
        </Button>
      </div>
    </section>
  );
}
