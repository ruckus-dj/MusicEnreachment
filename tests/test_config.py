from __future__ import annotations

from pathlib import Path
from shutil import copytree

import pytest
from pydantic import ValidationError

from music_ingest import __main__ as command
from music_ingest.config.policies import PolicyYamlError, load_policy_bundle, render_safe_summary

FIXTURE_DIRECTORY = Path(__file__).parent / 'fixtures' / 'policies' / 'valid'


def copied_policy_directory(tmp_path: Path) -> Path:
    destination = tmp_path / 'policies'
    copytree(FIXTURE_DIRECTORY, destination)
    return destination


def test_load_policy_bundle_when_valid_policies_are_supplied(tmp_path: Path) -> None:
    # Given: a complete repository-safe operator policy directory.
    policy_directory = copied_policy_directory(tmp_path)

    # When: the typed boundary loader reads every required policy.
    bundle = load_policy_bundle(policy_directory)

    # Then: canonical metadata policy and disabled providers are preserved.
    assert bundle.field_policy.list_separator == '; '
    assert bundle.providers.acoustid.enabled is False
    assert bundle.genre_policy.aliases['rap rock'] == ('Hip Hop', 'Alternative Rock')


def test_load_policy_bundle_when_invalid_user_agent_is_supplied(tmp_path: Path) -> None:
    # Given: a policy with an empty non-contactable MusicBrainz user agent.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        providers.read_text().replace('music-ingest/0.1 (operator@example.test)', ''), encoding='utf-8'
    )

    # When: the typed boundary loader reads the provider policy.
    # Then: it rejects the policy before any provider can be constructed.
    with pytest.raises(ValidationError, match='user_agent'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_musicbrainz_endpoint_is_not_fixed_or_public(tmp_path: Path) -> None:
    # Given: a policy targeting a local MusicBrainz endpoint.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        providers.read_text().replace('https://musicbrainz.org/ws/2/', 'http://localhost/ws/2/'), encoding='utf-8'
    )

    # When: the typed boundary loader reads the provider policy.
    # Then: only the public v2 endpoint is accepted.
    with pytest.raises(ValidationError, match='endpoint'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_an_unapproved_tag_is_supplied(tmp_path: Path) -> None:
    # Given: a field policy containing a raw source tag.
    policy_directory = copied_policy_directory(tmp_path)
    fields = policy_directory / 'field-policy.yaml'
    fields.write_text(f'{fields.read_text()}  - COMMENT\n', encoding='utf-8')

    # When: the typed boundary loader reads the field policy.
    # Then: the unapproved output tag is rejected.
    with pytest.raises(ValidationError, match='allowed_tag_keys'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_a_genre_alias_targets_an_unknown_genre(tmp_path: Path) -> None:
    # Given: an alias that would introduce an uncontrolled genre spelling.
    policy_directory = copied_policy_directory(tmp_path)
    genres = policy_directory / 'genre-policy.yaml'
    genres.write_text(
        genres.read_text().replace('  - Alternative Rock\n  rock rap:', '  - Uncontrolled Genre\n  rock rap:'),
        encoding='utf-8',
    )

    # When: the typed boundary loader reads the controlled vocabulary.
    # Then: aliases may only decompose to configured canonical genres.
    with pytest.raises(ValidationError, match='aliases'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_enabled_acoustid_has_no_secret_reference(tmp_path: Path) -> None:
    # Given: AcoustID enabled without an Infisical secret reference.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(providers.read_text().replace('enabled: false', 'enabled: true'), encoding='utf-8')

    # When: the typed boundary loader reads the provider policy.
    # Then: enabled AcoustID cannot proceed without its reference.
    with pytest.raises(ValidationError, match='secret_reference'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_providers_are_disabled_for_local_only_operation(tmp_path: Path) -> None:
    # Given: a policy that explicitly disables both network providers.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        providers.read_text().replace(
            'music-ingest/0.1 (operator@example.test)', 'music-ingest/0.1 (operator@example.test)\n  enabled: false'
        ),
        encoding='utf-8',
    )

    # When: the typed boundary loader reads local-only operation settings.
    bundle = load_policy_bundle(policy_directory)

    # Then: disabled providers leave the service ready for local-only review.
    assert bundle.providers.musicbrainz.enabled is False
    assert bundle.providers.acoustid.enabled is False


def test_load_policy_bundle_when_a_nested_mapping_has_duplicate_keys(tmp_path: Path) -> None:
    # Given: a provider policy with a duplicate key within a nested mapping.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        providers.read_text().replace(
            '  endpoint: https://musicbrainz.org/ws/2/',
            '  endpoint: https://musicbrainz.org/ws/2/\n  endpoint: https://musicbrainz.org/ws/2/',
        ),
        encoding='utf-8',
    )

    # When: YAML nodes are converted to the typed policy input.
    # Then: duplicate scalar keys cannot overwrite one another.
    with pytest.raises(PolicyYamlError, match='duplicate mapping key'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_disabled_acoustid_has_a_raw_secret_reference(tmp_path: Path) -> None:
    # Given: disabled AcoustID with a raw non-Infisical reference.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(f'{providers.read_text()}  secret_reference: raw-sentinel-reference\n', encoding='utf-8')

    # When: the provider policy is parsed.
    # Then: references are constrained even while the provider is disabled.
    with pytest.raises(ValidationError, match='secret_reference'):
        load_policy_bundle(policy_directory)


def test_load_policy_bundle_when_yaml_is_malformed(tmp_path: Path) -> None:
    # Given: malformed YAML containing a sentinel that must not reach diagnostics.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        f'{providers.read_text()}  secret_reference: raw-sentinel-reference\n  broken: [\n', encoding='utf-8'
    )

    # When: the YAML boundary parser encounters the malformed document.
    # Then: it returns a typed, value-redacted parsing error.
    with pytest.raises(PolicyYamlError) as error:
        load_policy_bundle(policy_directory)
    assert str(providers) in str(error.value)
    assert 'invalid YAML document' in str(error.value)
    assert 'raw-sentinel-reference' not in str(error.value)


def test_acoustid_secret_reference_is_excluded_from_ordinary_serialization(tmp_path: Path) -> None:
    # Given: enabled AcoustID using an Infisical reference.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    providers.write_text(
        providers.read_text().replace(
            'enabled: false', 'enabled: true\n  secret_reference: infisical://music-ingest/acoustid-client-key'
        ),
        encoding='utf-8',
    )

    # When: ordinary and safe policy serializations are requested.
    bundle = load_policy_bundle(policy_directory)

    # Then: neither output contains the operational reference.
    assert 'secret_reference' not in bundle.model_dump()['providers']['acoustid']
    assert 'infisical://' not in render_safe_summary(bundle)


def test_cli_when_acoustid_reference_is_raw_redacts_stderr(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: a raw AcoustID reference that Pydantic rejects.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    _ = providers.write_text(f'{providers.read_text()}  secret_reference: raw-cli-sentinel\n', encoding='utf-8')

    # When: the real module CLI loads the invalid policy directory.
    monkeypatch.setattr(command, 'argv', ['music-ingest', str(policy_directory)])
    with pytest.raises(SystemExit) as result:
        command.main()

    # Then: process failure exposes only a stable generic error.
    assert result.value.code == 'music-ingest: invalid policy configuration'
    assert 'raw-cli-sentinel' not in str(result.value.code)


def test_cli_when_yaml_is_malformed_redacts_stderr(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    # Given: malformed YAML containing a sentinel value.
    policy_directory = copied_policy_directory(tmp_path)
    providers = policy_directory / 'metadata-providers.yaml'
    _ = providers.write_text(
        f'{providers.read_text()}  secret_reference: raw-cli-sentinel\n  broken: [\n', encoding='utf-8'
    )

    # When: the real module CLI loads the malformed directory.
    monkeypatch.setattr(command, 'argv', ['music-ingest', str(policy_directory)])
    with pytest.raises(SystemExit) as result:
        command.main()

    # Then: parse details and raw YAML values remain redacted.
    assert result.value.code == 'music-ingest: invalid policy configuration'
    assert 'raw-cli-sentinel' not in str(result.value.code)


def test_load_policy_bundle_when_yaml_uses_a_python_object_tag(tmp_path: Path) -> None:
    # Given: a policy document containing an unsafe Python-object YAML tag.
    policy_directory = copied_policy_directory(tmp_path)
    fields = policy_directory / 'field-policy.yaml'
    _ = fields.write_text(
        f'{fields.read_text()}unsafe: !!python/object/apply:builtins.str [unsafe]\n', encoding='utf-8'
    )

    # When: the SafeLoader node boundary parses the document.
    # Then: no Python object is constructed and the unsupported tag is rejected.
    with pytest.raises(PolicyYamlError, match='unsupported YAML tag'):
        _ = load_policy_bundle(policy_directory)
