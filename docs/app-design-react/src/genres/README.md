# MusicBrainz genre snapshot

`musicbrainzCatalog.json` is a fixed snapshot of the genre links in the primary
MusicBrainz genres page, retrieved on 2026-11-03:
<https://musicbrainz.org/genres>. The page response contained 2,220 distinct
genre UUID links; names are stored as returned by the page and normalized for
display at load time. This is an offline prototype fixture, not a live catalog
or a promise that the data is current.

MusicBrainz lists genre names and MBIDs as core database data, and describes core
database data as CC0. See the official
[MusicBrainz Database](https://musicbrainz.org/doc/MusicBrainz_Database) and
[Data License](https://musicbrainz.org/doc/About/Data_License) documentation.
The snapshot is attributed to MusicBrainz and MetaBrainz Foundation. The source
spelling and MBID are retained so consumers can distinguish display formatting
from source data and identity.
