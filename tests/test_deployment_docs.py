from __future__ import annotations

from pathlib import Path

ROOT = Path(__file__).parents[1]


def test_only_komodo_describes_production_deployment_and_templates_have_no_secrets() -> None:
    stack = ROOT / 'komodo' / 'music-ingest.stack.yaml'
    runbook = ROOT / 'docs' / 'deployment.md'
    assert stack.exists()
    assert runbook.exists()
    assert 'Komodo' in runbook.read_text(encoding='utf-8')
    assert 'docker compose' not in runbook.read_text(encoding='utf-8').lower()
    assert not list(ROOT.glob('*compose*.y*ml'))
    for template in (ROOT / 'config' / 'templates').glob('*.yaml'):
        content = template.read_text(encoding='utf-8').lower()
        assert 'password:' not in content
        assert 'api_key:' not in content
        assert '.env' not in content


def test_operational_docs_preserve_dry_run_and_provider_safety_contracts() -> None:
    content = (ROOT / 'docs' / 'deployment.md').read_text(encoding='utf-8')
    assert 'python -m music_ingest dry-run' in content
    assert 'records unresolved states on the stable library record' in content
    assert 'MUSIC_INGEST_ENABLE_LIVE_TESTS=1' in content
    assert 'never removes `.nfo`' in content


def test_operational_docs_describe_storage_and_publication_contracts() -> None:
    content = (ROOT / 'docs' / 'deployment.md').read_text(encoding='utf-8')
    assert '`reserved` or `staged`' in content
    assert 'atomically replaced' in content
