"""Typed YAML policy boundary for the music ingestion service."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import ClassVar, Final, Literal, override

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator
from yaml.loader import SafeLoader
from yaml.nodes import MappingNode, Node, ScalarNode, SequenceNode
from yaml.parser import ParserError
from yaml.reader import ReaderError
from yaml.scanner import ScannerError

MUSICBRAINZ_ENDPOINT: Final = 'https://musicbrainz.org/ws/2/'
ALLOWED_TAG_KEYS: Final = frozenset(
    {
        'TITLE',
        'ARTIST',
        'ALBUM',
        'ALBUMARTIST',
        'DATE',
        'ORIGINALDATE',
        'TRACKNUMBER',
        'TRACKTOTAL',
        'DISCNUMBER',
        'DISCTOTAL',
        'GENRE',
        'MUSICBRAINZ_TRACKID',
        'MUSICBRAINZ_ALBUMID',
        'MUSICBRAINZ_RELEASEGROUPID',
        'ISRC',
    }
)
YAML_NODE_TAGS: Final = frozenset(
    {
        'tag:yaml.org,2002:map',
        'tag:yaml.org,2002:seq',
        'tag:yaml.org,2002:str',
        'tag:yaml.org,2002:null',
        'tag:yaml.org,2002:bool',
        'tag:yaml.org,2002:int',
        'tag:yaml.org,2002:float',
    }
)


class FrozenPolicy(BaseModel):
    """Immutable, unknown-field-free policy input."""

    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    schema_version: Literal[1]


class StorageLocations(BaseModel):
    """Locations the service may use after an operator deploys it."""

    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    work_dir: Path
    quarantine_dir: Path
    provenance_dir: Path


class ReviewPolicy(BaseModel):
    """Default human-review routing controls."""

    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)

    manual_review_on_low_confidence: Literal[True]
    manual_review_on_unknown_genre: Literal[True]
    manual_review_on_lyric_mismatch: Literal[True]


class ConfigurationPolicy(FrozenPolicy):
    """Deployment-safe local locations and default review routing."""

    storage: StorageLocations
    review: ReviewPolicy


class FieldPolicy(FrozenPolicy):
    """Canonical output tags and serialization contract."""

    list_separator: Literal['; ']
    allowed_tag_keys: tuple[str, ...]

    @field_validator('allowed_tag_keys')
    @classmethod
    def allowed_tag_keys_are_exact(cls, keys: tuple[str, ...]) -> tuple[str, ...]:
        if frozenset(keys) != ALLOWED_TAG_KEYS or len(keys) != len(ALLOWED_TAG_KEYS):
            raise ValueError('allowed_tag_keys must contain exactly the canonical output keys')
        return keys


class GenrePolicy(FrozenPolicy):
    """Controlled canonical genres and source alias decompositions."""

    canonical_genres: tuple[str, ...]
    aliases: Mapping[str, tuple[str, ...]]

    @model_validator(mode='after')
    def aliases_target_canonical_genres(self) -> GenrePolicy:
        canonical_genres = frozenset(self.canonical_genres)
        if not canonical_genres:
            raise ValueError('canonical_genres must not be empty')
        if any(not alias.strip() for alias in self.aliases):
            raise ValueError('aliases must have non-empty source names')
        if any(
            not target or any(genre not in canonical_genres for genre in target) for target in self.aliases.values()
        ):
            raise ValueError('aliases must target only configured canonical_genres')
        return self


class ProviderSettings(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='forbid', frozen=True)


class MusicBrainzPolicy(ProviderSettings):
    """The only supported metadata authority endpoint and cache policy."""

    enabled: bool = True
    endpoint: Literal['https://musicbrainz.org/ws/2/']
    user_agent: str = Field(min_length=1)
    rate_limit_seconds: Literal[1]
    cache_freshness_hours: Literal[24]

    @field_validator('user_agent')
    @classmethod
    def user_agent_has_operator_contact(cls, user_agent: str) -> str:
        contact_start = user_agent.find('(')
        contact_end = user_agent.find(')', contact_start + 1)
        contact = user_agent[contact_start + 1 : contact_end]
        if not user_agent.strip() or contact_start < 1 or contact_end < 0 or '@' not in contact:
            raise ValueError('user_agent must include a contact email in parentheses')
        return user_agent


class AcoustIdPolicy(ProviderSettings):
    """Optional evidence-only AcoustID settings without credential values."""

    enabled: bool = False
    secret_reference: str | None = Field(default=None, exclude=True)
    rate_limit_seconds: Literal[1] = 1

    @field_validator('secret_reference')
    @classmethod
    def secret_reference_is_an_infisical_reference(cls, reference: str | None) -> str | None:
        if reference is not None and not reference.startswith('infisical://'):
            raise ValueError('secret_reference must be an Infisical reference')
        return reference

    @model_validator(mode='after')
    def enabled_policy_requires_secret_reference(self) -> AcoustIdPolicy:
        if self.enabled and self.secret_reference is None:
            raise ValueError('secret_reference must be an Infisical reference when AcoustID is enabled')
        return self


class ProviderPolicy(FrozenPolicy):
    """Network provider controls that preserve a local-only operating mode."""

    musicbrainz: MusicBrainzPolicy
    acoustid: AcoustIdPolicy


class LyricsPolicy(FrozenPolicy):
    """External-only lyric publication policy."""

    output_format: Literal['lrc']
    embedded_lyrics: Literal[False]
    manual_review_on_mismatch: Literal[True]


class ArtworkPolicy(FrozenPolicy):
    """Release-sidecar artwork policy."""

    output_format: Literal['jpg', 'webp']
    embed_in_flac: Literal[False]
    one_file_per_release: Literal[True]


class PolicyBundle(BaseModel):
    """All policy files parsed once at the operator configuration boundary."""

    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    storage: StorageLocations
    review: ReviewPolicy
    field_policy: FieldPolicy
    genre_policy: GenrePolicy
    providers: ProviderPolicy
    lyrics_policy: LyricsPolicy
    artwork_policy: ArtworkPolicy


def load_policy_bundle(policy_directory: Path) -> PolicyBundle:
    """Load all required YAML policies from an operator-supplied directory."""
    config_policy = _load_policy(policy_directory / 'config.yaml', ConfigurationPolicy)
    return PolicyBundle(
        storage=config_policy.storage,
        review=config_policy.review,
        field_policy=_load_policy(policy_directory / 'field-policy.yaml', FieldPolicy),
        genre_policy=_load_policy(policy_directory / 'genre-policy.yaml', GenrePolicy),
        providers=_load_policy(policy_directory / 'metadata-providers.yaml', ProviderPolicy),
        lyrics_policy=_load_policy(policy_directory / 'lyrics-policy.yaml', LyricsPolicy),
        artwork_policy=_load_policy(policy_directory / 'artwork-policy.yaml', ArtworkPolicy),
    )


def render_safe_summary(bundle: PolicyBundle) -> str:
    """Return configuration state that never includes secret reference values."""
    return bundle.model_dump_json(
        include={
            'field_policy': {'list_separator'},
            'providers': {'musicbrainz': {'enabled', 'endpoint'}, 'acoustid': {'enabled'}},
        }
    )


def _load_policy[PolicyModel: FrozenPolicy](path: Path, model: type[PolicyModel]) -> PolicyModel:
    try:
        with path.open(encoding='utf-8') as policy_file:
            policy_node = SafeLoader(policy_file).get_single_node()
    except (ParserError, ReaderError, ScannerError) as error:
        raise PolicyYamlError(path=path, description='invalid YAML document') from error
    if policy_node is None:
        raise PolicyYamlError(path=path, description='a policy file must contain one YAML document')
    parsed_policy = _yaml_value(policy_node, path)
    return model.model_validate(parsed_policy)


type YamlValue = str | int | float | bool | None | list['YamlValue'] | dict[str, 'YamlValue']


@dataclass(frozen=True, slots=True)
class PolicyYamlError(Exception):
    path: Path
    description: str

    @override
    def __str__(self) -> str:
        return f'{self.path}: {self.description}'


def _yaml_value(node: Node, path: Path) -> YamlValue:
    if node.tag not in YAML_NODE_TAGS:
        raise PolicyYamlError(path=path, description='unsupported YAML tag')
    match node:
        case ScalarNode(tag='tag:yaml.org,2002:null'):
            return None
        case ScalarNode(tag='tag:yaml.org,2002:bool'):
            return _scalar_text(node, path).lower() == 'true'
        case ScalarNode(tag='tag:yaml.org,2002:int'):
            return int(_scalar_text(node, path))
        case ScalarNode(tag='tag:yaml.org,2002:float'):
            return float(_scalar_text(node, path))
        case ScalarNode():
            return _scalar_text(node, path)
        case SequenceNode():
            return [_yaml_value(item, path) for item in _sequence_nodes(node, path)]
        case MappingNode():
            mapping: dict[str, YamlValue] = {}
            for key, value in _mapping_pairs(node, path):
                name = _yaml_key(key, path)
                if name in mapping:
                    raise PolicyYamlError(path=path, description='duplicate mapping key')
                mapping[name] = _yaml_value(value, path)
            return mapping
        case _:
            raise PolicyYamlError(path=path, description='unsupported YAML node')


def _yaml_key(node: Node, path: Path) -> str:
    match node:
        case ScalarNode():
            return _scalar_text(node, path)
        case _:
            raise PolicyYamlError(path=path, description='policy mappings require scalar keys')


def _scalar_text(node: ScalarNode, path: Path) -> str:
    value: object = node.value
    if not isinstance(value, str):
        raise PolicyYamlError(path=path, description='scalar YAML values must be strings')
    return value


def _sequence_nodes(node: SequenceNode, path: Path) -> list[Node]:
    value: object = node.value
    if not isinstance(value, list):
        raise PolicyYamlError(path=path, description='invalid YAML sequence')
    nodes: list[Node] = []
    for item in value:
        if not isinstance(item, Node):
            raise PolicyYamlError(path=path, description='invalid YAML sequence')
        nodes.append(item)
    return nodes


def _mapping_pairs(node: MappingNode, path: Path) -> list[tuple[Node, Node]]:
    value: object = node.value
    if not isinstance(value, list):
        raise PolicyYamlError(path=path, description='invalid YAML mapping')
    pairs: list[tuple[Node, Node]] = []
    for pair in value:
        if not isinstance(pair, tuple) or len(pair) != 2:
            raise PolicyYamlError(path=path, description='invalid YAML mapping')
        key, item = pair
        if not isinstance(key, Node) or not isinstance(item, Node):
            raise PolicyYamlError(path=path, description='invalid YAML mapping')
        pairs.append((key, item))
    return pairs
