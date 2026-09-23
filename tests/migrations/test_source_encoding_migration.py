from pathlib import Path

from alembic.config import Config
from sqlalchemy import create_engine, inspect

from alembic import command
from tests.support.paths import ALEMBIC_DIRECTORY


def test_encoding_migration_adds_nullable_evidence_without_source_io(tmp_path: Path) -> None:
    config = Config()
    config.set_main_option('script_location', str(ALEMBIC_DIRECTORY))
    url = f'sqlite:///{tmp_path / "encoding.db"}'
    config.set_main_option('sqlalchemy.url', url)
    command.upgrade(config, '20260916_0028')
    engine = create_engine(url)
    from sqlalchemy import text

    with engine.begin() as connection:
        connection.execute(
            text(
                'INSERT INTO source_tag_observations (source_id, format_name, tag_name, value, '
                "applied_choice_json) VALUES ('legacy', 'ID3v2', 'TITLE', 'Original', :choice)"
            ),
            {'choice': '{"field_id":1,"mode":"original"}'},
        )
    command.upgrade(config, 'head')
    with engine.connect() as connection:
        assert connection.scalar(text('SELECT decision_origin FROM source_tag_observations')) == 'manual'
    columns = {item['name']: item for item in inspect(engine).get_columns('source_tag_observations')}
    assert columns['prepared_bytes']['nullable'] is True
    assert columns['binary_evidence']['nullable'] is True
    assert columns['original_value']['nullable'] is True
    assert columns['extraction_version']['nullable'] is True
    assert 'source_metadata_revision' in {item['name'] for item in inspect(engine).get_columns('jobs')}
    engine.dispose()
