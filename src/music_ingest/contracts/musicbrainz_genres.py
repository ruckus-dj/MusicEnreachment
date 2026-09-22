from pydantic import BaseModel, ConfigDict, Field


class GenrePayload(BaseModel):
    model_config = ConfigDict(frozen=True)

    musicbrainz_id: str = Field(alias='id', min_length=1)
    source_name: str = Field(alias='name', min_length=1)


class GenrePage(BaseModel):
    model_config = ConfigDict(frozen=True)

    count: int = Field(alias='genre-count', ge=0)
    offset: int = Field(alias='genre-offset', ge=0)
    genres: tuple[GenrePayload, ...]
