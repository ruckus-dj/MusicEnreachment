from typing import Literal, Self

from pydantic import BaseModel, ConfigDict, Field, model_validator

Codec = Literal['utf-8', 'utf-16', 'utf-16-le', 'utf-16-be', 'latin-1', 'cp1251', 'cp1252', 'koi8-r', 'cp866']


class EncodingChoice(BaseModel):
    model_config = ConfigDict(extra='forbid')
    field_id: int = Field(gt=0)
    mode: Literal['keep', 'original', 'decode', 'unicode', 'codec'] = 'keep'
    encode_codec: Codec | None = None
    decode_codec: Codec | None = None

    @model_validator(mode='after')
    def valid_chain(self) -> Self:
        if self.mode in {'keep', 'original'} and (self.encode_codec or self.decode_codec):
            raise ValueError('keep/original do not accept codecs')
        if self.mode in {'decode', 'codec'} and (self.encode_codec is not None or self.decode_codec is None):
            raise ValueError('decode requires only decode_codec')
        if self.mode == 'unicode' and (self.encode_codec is None or self.decode_codec is None):
            raise ValueError('unicode requires encode_codec and decode_codec')
        return self


class EncodingRequest(BaseModel):
    model_config = ConfigDict(extra='forbid')
    expected_revision: int = Field(ge=1)
    choices: list[EncodingChoice] = Field(min_length=1, max_length=256)

    @model_validator(mode='after')
    def unique_fields(self) -> Self:
        if len({choice.field_id for choice in self.choices}) != len(self.choices):
            raise ValueError('duplicate field_id')
        return self


class EncodingPreviewField(BaseModel):
    field_id: int
    value: str | None
    status: Literal['unchanged', 'changed', 'error']
    error: str | None = None


class EncodingPreview(BaseModel):
    source_id: str
    source_revision: int
    valid: bool
    fields: list[EncodingPreviewField]


class EncodingField(BaseModel):
    field_id: int
    tag_name: str
    container: str
    physical_id: str | None
    extraction_version: str | None
    selected: bool
    original_value: str
    current_value: str
    raw_evidence_available: bool
    declared_codec: str | None
    applied_choice: EncodingChoice | None
    decision_origin: str | None = None


class EncodingSuggestion(BaseModel):
    field_id: int
    state: Literal['suggested', 'review']
    value: str | None
    choice: EncodingChoice | None
    reason: str


class EncodingDetail(BaseModel):
    source_id: str
    source_revision: int
    fields: list[EncodingField]
    suggestions: list[EncodingSuggestion] = Field(default_factory=list)


class EncodingApplied(EncodingPreview):
    queued: bool
