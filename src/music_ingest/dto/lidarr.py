from pathlib import Path
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field


class LidarrTrackFile(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    path: Path


class LidarrRenamedTrackFile(LidarrTrackFile):
    previous_path: Path = Field(alias='previousPath')


class LidarrAlbum(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    id: int | str


class LidarrTestPayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Test'] = Field(alias='eventType')


class LidarrDownloadPayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Download', 'ReleaseImport'] = Field(alias='eventType')
    track_files: tuple[LidarrTrackFile, ...] = Field(alias='trackFiles', min_length=1)
    is_upgrade: bool = Field(alias='isUpgrade')
    deleted_files: tuple[LidarrTrackFile, ...] = Field(default=(), alias='deletedFiles')


class LidarrRenamePayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['Rename'] = Field(alias='eventType')
    renamed_track_files: tuple[LidarrRenamedTrackFile, ...] = Field(alias='renamedTrackFiles', min_length=1)


class LidarrAlbumDeletePayload(BaseModel):
    model_config = ConfigDict(extra='allow', frozen=True)

    event_type: Literal['AlbumDelete'] = Field(alias='eventType')
    album: LidarrAlbum
    deleted_files: bool = Field(alias='deletedFiles')


type LidarrEvent = Annotated[
    LidarrTestPayload | LidarrDownloadPayload | LidarrRenamePayload | LidarrAlbumDeletePayload,
    Field(discriminator='event_type'),
]


class LidarrIntakeError(ValueError):
    pass
