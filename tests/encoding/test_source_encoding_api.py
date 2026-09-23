from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker
from sqlalchemy.pool import StaticPool

from music_ingest.api.routers.metadata import create_router
from music_ingest.models import Base, SourceRecord, SourceTagRecord


def test_encoding_api_database_only_preview_apply_and_readback() -> None:
    engine = create_engine('sqlite://', connect_args={'check_same_thread': False}, poolclass=StaticPool)
    Base.metadata.create_all(engine)
    factory = sessionmaker(engine)
    with Session(engine) as session:
        source = SourceRecord(
            id='source',
            source_path='/absent',
            device=1,
            inode=1,
            size_bytes=1,
            sha256='a' * 64,
            origin='manual',
            intake_state='present',
        )
        source.tag_observations.append(SourceTagRecord(format_name='mp3', tag_name='TITLE', value='Ïðèâåò'))
        session.add(source)
        session.commit()
    app = FastAPI()
    app.include_router(create_router(factory))
    with TestClient(app) as client:
        detail = client.get('/api/sources/source/encoding')
        assert detail.status_code == 200
        field = detail.json()['fields'][0]
        assert field['raw_evidence_available'] is False
        suggestion = detail.json()['suggestions'][0]
        assert suggestion['value'] == 'Привет'
        assert suggestion['state'] == 'suggested'
        assert suggestion['choice']['mode'] == 'unicode'
        assert field['current_value'] == 'Ïðèâåò'
        request = {
            'expected_revision': 1,
            'choices': [
                {'field_id': field['field_id'], 'mode': 'unicode', 'encode_codec': 'latin-1', 'decode_codec': 'cp1251'}
            ],
        }
        preview = client.post('/api/sources/source/encoding/preview', json=request)
        assert preview.status_code == 200
        assert preview.json()['fields'][0]['value'] == 'Привет'
        assert client.get('/api/sources/source/encoding').json()['source_revision'] == 1
        applied = client.post('/api/sources/source/encoding/apply', json=request)
        assert applied.status_code == 200
        assert applied.json()['queued'] is True
        assert client.get('/api/sources/source/encoding').json()['fields'][0]['current_value'] == 'Привет'
        assert client.post('/api/sources/source/encoding/apply', json=request).status_code == 409
        request['expected_revision'] = 2
        request['choices'][0] = {'field_id': field['field_id'], 'mode': 'decode', 'decode_codec': 'utf-8'}
        assert client.post('/api/sources/source/encoding/apply', json=request).status_code == 422
        assert client.get('/api/sources/missing/encoding').status_code == 404
