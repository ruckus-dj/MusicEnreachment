from typing import ClassVar

from pydantic import BaseModel, ConfigDict


class Recording(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    id: str


class Result(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    score: float
    recordings: tuple[Recording, ...] = ()


class Response(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(extra='ignore', frozen=True)

    status: str
    results: tuple[Result, ...] = ()
