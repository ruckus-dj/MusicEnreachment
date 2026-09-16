import pytest

from music_ingest.dto.source_encoding import EncodingChoice, EncodingField
from music_ingest.normalize.encoding_suggestions import suggest_encodings


def field(value: str, name: str = 'TITLE', number: int = 1, container: str = 'ID3v2') -> EncodingField:
    return EncodingField(
        field_id=number,
        tag_name=name,
        container=container,
        physical_id=None,
        extraction_version=None,
        selected=True,
        original_value=value,
        current_value=value,
        raw_evidence_available=False,
        declared_codec='utf-16-le' if container == 'ASF' else 'latin-1',
        applied_choice=None,
    )


def damaged(text: str) -> str:
    return text.encode('cp1251').decode('latin-1')


@pytest.mark.parametrize('title,artist', [('Морячек', None), ('Свобода', 'Ангус'), ('Водка и вино', 'Фиги')])
def test_persisted_track_context(title: str, artist: str | None) -> None:
    fields = [field(damaged(title))]
    if artist:
        fields.append(field(damaged(artist), 'ARTIST', 2))
    proposals = suggest_encodings(fields)
    assert [p.value for p in proposals] == ([title, artist] if artist else [title])
    for proposal, original in zip(proposals, fields, strict=True):
        assert proposal.state == 'suggested'
        assert proposal.choice is not None
        assert proposal.choice.mode == 'unicode'
        assert proposal.choice.encode_codec is not None
        assert proposal.choice.decode_codec is not None
        assert (
            original.current_value.encode(proposal.choice.encode_codec).decode(proposal.choice.decode_codec)
            == proposal.value
        )


@pytest.mark.parametrize('value', ['Beyoncé', 'Motörhead', 'Île', 'Don’t', 'Привет', 'Блики', '', '    ', '\ufffd'])
@pytest.mark.parametrize('container', ['ID3v2', 'ASF', 'FLAC'])
def test_correct_or_lost_text_unchanged_even_with_context(value: str, container: str) -> None:
    fields = [field(value, container=container), field(damaged('Свобода'), 'ALBUM', 2, container)]
    assert all(proposal.field_id != 1 for proposal in suggest_encodings(fields))


@pytest.mark.parametrize('value', ['Î5', damaged('Фиги')])
def test_short_is_review_not_chosen(value: str) -> None:
    proposal = suggest_encodings([field(value)])[0]
    assert proposal.state == 'review'
    assert proposal.choice is None
    assert proposal.value is None


def test_duplicate_field_or_other_block_is_not_context() -> None:
    short = field(damaged('Фиги'))
    assert suggest_encodings([short, field(damaged('Свобода'), 'TITLE', 2)])[0].state == 'review'
    assert suggest_encodings([short, field(damaged('Свобода'), 'ALBUM', 2, 'ID3v1')])[0].state == 'review'


def test_saved_manual_decision_is_not_overridden() -> None:
    saved = field(damaged('Морячек'))
    saved.applied_choice = EncodingChoice(field_id=1, mode='original')
    assert suggest_encodings([saved]) == []


def test_shadow_title_is_not_context_for_selected_artist() -> None:
    artist = field(damaged('Фиги'), 'ARTIST', 1)
    title = field('Correct title', 'TITLE', 2)
    shadow = field(damaged('Свобода'), 'TITLE', 3)
    shadow.selected = False
    proposal = next(item for item in suggest_encodings([artist, title, shadow]) if item.field_id == 1)
    assert proposal.state == 'review'
    assert proposal.choice is None
