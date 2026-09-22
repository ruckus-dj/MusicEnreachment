from typing import ClassVar

from pydantic import BaseModel, ConfigDict, Field


class FpcalcPayload(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True, strict=True)

    duration: float = Field(ge=0)
    fingerprint: str = Field(min_length=1)
