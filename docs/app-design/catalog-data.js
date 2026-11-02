const demoArtists = [
  { id: 'mira', name: 'Mira Vale', genre: 'Ambient / Folk', color: 'clay' },
  { id: 'arden', name: 'Arden Trio', genre: 'Contemporary jazz', color: 'forest' },
  { id: 'forma', name: 'Forma', genre: 'Electronic', color: 'dusk' },
  { id: 'various', name: 'Various Artists', genre: 'Локальные сборники', color: 'sand' }
];
const demoAlbums = [
  { id: 'north', title: 'Northern Lines', artistId: 'mira', year: '2021', country: 'XE', label: 'Field Records', catalog: 'FR-021', color: 'clay', providerId: 'p-north', titles: ['First Light', 'Still Water', 'Northbound', 'The Long Way Home', 'Pine Forest', 'Under the Bridge', 'Low Tide', 'Open Country', 'Winter Birds', 'Last Train'], durations: [252, 308, 226, 381, 264, 198, 291, 244, 318, 207] },
  { id: 'hours', title: 'Small Hours', artistId: 'arden', year: '2018', country: 'GB', label: 'Room Tone', catalog: 'RT-018', color: 'forest', providerId: 'p-hours', titles: ['Green Room', 'After Midnight', 'Slow Motion', 'Blue Window', 'On the Corner', 'Paper Moon', 'Before Dawn', 'Small Hours'], durations: [284, 312, 241, 298, 329, 246, 271, 356] },
  { id: 'signals', title: 'Signals', artistId: 'forma', year: '2024', country: 'XW', label: 'Independent', catalog: 'FM-024', color: 'dusk', providerId: 'p-signals', titles: ['Carrier', 'Relay', 'Frequency', 'Return Path', 'Static', 'Signal Lost'], durations: [198, 243, 287, 226, 214, 272] },
  { id: 'rooms', title: 'Collected Rooms', artistId: 'mira', year: '2020', country: 'XE', label: 'Field Records', catalog: 'FR-020', color: 'slate', providerId: 'p-rooms', titles: ['Morning Room', 'North Window', 'Night Sketch', 'Half Light', 'Stone Steps', 'Second Floor', 'Quiet Street', 'Home Again'], durations: [229, 301, 254, 217, 284, 266, 246, 329] },
  { id: 'mix', title: 'Road Notes — личный сборник', artistId: 'various', year: '2023', country: '', label: '', catalog: '', color: 'sand', providerId: null, titles: ['First Light', 'Relay', 'Green Room', 'Night Sketch', 'Unknown Signal'], durations: [252, 243, 284, 254, 239] }
];
const demoFiles = {};
const providerReleases = [];
const localReleases = [];
// Explicit recording metadata fixtures, not an artist/release inheritance rule.
const demoRecordingGenres = {
  north: ['ambient', 'folk'], hours: ['contemporary jazz'],
  signals: ['electronic'], rooms: ['ambient', 'folk']
};

function demoArtist(id) { return demoArtists.find(artist => artist.id === id); }

function providerTags(album, track) {
  const artist = demoArtist(track.artistId || album.artistId);
  const artistNameValue = artist?.name || '';
  return {
    TITLE: track.title ? [track.title] : [], ARTIST: artistNameValue ? [artistNameValue] : [], ARTISTSORT: artistNameValue ? [artistNameValue] : [],
    ALBUM: album.title ? [album.title] : [], ALBUMARTIST: album.credits?.length ? album.credits.map(credit => credit.credit || credit.name).filter(Boolean) : (demoArtist(album.artistId)?.name ? [demoArtist(album.artistId).name] : []),
    DATE: [album.year], COUNTRY: album.country ? [album.country] : [],
    LABEL: album.label ? [album.label] : [], CATALOGNUMBER: album.catalog ? [album.catalog] : [],
    TRACKNUMBER: [String(track.displayedNumber || track.mediumPosition || track.position)], DISCNUMBER: [String(track.medium ?? 1)], GENRE: [...(track.recordingGenres || [])],
    MUSICBRAINZ_ARTISTID: album.manual ? [] : artist ? [`demo-artist-${artist.id}`] : [],
    MUSICBRAINZ_ALBUMID: album.providerId ? [album.providerId] : [],
    MUSICBRAINZ_TRACKID: album.manual ? [] : track.recordingId ? [track.recordingId] : [],
    COMMENT: []
  };
}

for (const album of demoAlbums) {
  const tracks = album.titles.map((title, index) => {
    const artistId = album.id === 'mix' ? ['mira', 'forma', 'arden', 'mira', 'various'][index] : album.artistId;
    return { id: `${album.id}-t${index + 1}`, position: index + 1, title, duration: album.durations[index], artistId, recordingId: album.providerId ? `demo-rec-${album.id}-${index + 1}` : null, recordingGenres: [...(demoRecordingGenres[album.id] || [])], sourceId: null, providerTrackId: null, published: null };
  });
  const local = { ...album, tracks };
  delete local.titles;
  delete local.durations;
  localReleases.push(local);
  if (album.providerId) {
    providerReleases.push({ ...local, id: album.providerId, medium: 'CD', tracks: tracks.map(track => ({ ...track, id: `${album.providerId}-t${track.position}` })) });
  }
  for (const track of tracks) {
    const available = album.id !== 'north' || track.position <= 8;
    if (!available) continue;
    const fileId = `${album.id}-f${track.position}`;
    const raw = providerTags(album, track);
    raw.GENRE = demoArtist(track.artistId)?.genre ? [demoArtist(track.artistId).genre] : [];
    raw.MUSICBRAINZ_ARTISTID = [];
    raw.MUSICBRAINZ_ALBUMID = [];
    raw.MUSICBRAINZ_TRACKID = [];
    raw.COUNTRY = [];
    raw.CATALOGNUMBER = [];
    raw.ARTISTSORT = [];
    if (album.id === 'north') {
      raw.ARTIST = ['mira vale']; raw.ALBUMARTIST = ['mira vale']; raw.ALBUM = ['Northern lines'];
      raw.DATE = ['2020']; raw.LABEL = []; raw.COMMENT = ['Original rip'];
      if (track.position === 3) raw.TITLE = ['Northbound (live?)'];
    }
    if (album.id === 'mix') {
      raw.ALBUM = ['Road Notes']; raw.ALBUMARTIST = ['Various Artists']; raw.DATE = ['2023']; raw.LABEL = []; raw.COMMENT = ['Personal compilation'];
    }
    demoFiles[fileId] = {
      id: fileId, name: `${String(track.position).padStart(2, '0')} - ${raw.TITLE[0]}.${album.id === 'mix' ? 'mp3' : 'flac'}`,
      path: `/mnt/inbox/${album.id}/${String(track.position).padStart(2, '0')} - ${raw.TITLE[0]}.${album.id === 'mix' ? 'mp3' : 'flac'}`,
      tags: raw, codec: album.id === 'mix' ? 'MP3' : 'FLAC', bits: 24, sampleRate: 48000,
      duration: track.duration + (album.id === 'north' && track.position === 3 ? 12 : 0),
      size: `${(22 + track.position * 2.3).toFixed(1)} MB`, available: true,
      providerTrackId: album.providerId ? `${album.providerId}-t${track.position}` : null,
      confidence: album.id === 'north' && track.position === 3 ? 'low' : 'high',
      fingerprint: album.id === 'north' && track.position === 3 ? 'Не найдено подтверждения' : 'Нет ответа AcoustID; fingerprint рассчитан'
    };
    track.sourceIds = [fileId];
    track.sourceId = fileId;
    track.providerTrackId = album.providerId ? `${album.providerId}-t${track.position}` : null;
    if (album.id === 'rooms' || album.id === 'signals' || (album.id === 'north' && track.position <= 5)) {
      const tags = structuredClone(raw);
      tags.GENRE = [...track.recordingGenres];
      track.published = { tags, coverHash: null, lyrics: null, codec: album.id === 'north' ? 'MP3' : 'FLAC', container: 'MKA', sourceId: album.id === 'north' ? `previous-mp3-${fileId}` : fileId, readAt: '26.09.2026 10:18', path: `/srv/music/${demoArtist(album.artistId).name}/${album.year} - ${album.title}/${String(track.position).padStart(2, '0')} - ${track.title}.mka` };
      track.metadataBaseline = structuredClone(track.published.tags);
      track.metadataBaselineExtras = { coverHash: track.published.coverHash, lyrics: track.published.lyrics };
    }
  }
}

const japan = structuredClone(providerReleases.find(release => release.id === 'p-north'));
japan.id = 'p-japan'; japan.providerId = 'p-japan'; japan.year = '2022'; japan.country = 'JP'; japan.catalog = 'FR-021-JP';
japan.tracks = japan.tracks.map(track => ({ ...track, id: `p-japan-t${track.position}` }));
japan.tracks.push({ id: 'p-japan-t11', position: 11, title: 'Afterglow (bonus)', duration: 267, artistId: 'mira', recordingId: 'demo-rec-north-bonus' });
providerReleases.splice(1, 0, japan);

const mix = localReleases.find(release => release.id === 'mix');
const mixLinks = ['p-north-t1', 'p-signals-t2', 'p-hours-t1', 'p-rooms-t3', null];
mix.tracks.forEach((track, index) => {
  demoFiles[track.sourceId].providerTrackId = mixLinks[index];
  demoFiles[track.sourceId].confidence = index < 2 ? 'high' : 'low';
  track.providerTrackId = index < 2 ? mixLinks[index] : null;
  track.recordingId = index < 2 ? providerReleases.flatMap(release => release.tracks).find(item => item.id === mixLinks[index]).recordingId : null;
});

demoFiles['north-extra'] = { ...structuredClone(demoFiles['north-f1']), id: 'north-extra', name: 'session fragment.flac', path: '/mnt/inbox/north/session fragment.flac', duration: 73, providerTrackId: null, confidence: 'low', tags: { TITLE: ['Session fragment'], ARTIST: ['Mira Vale'], ALBUM: ['Northern lines'], COMMENT: ['Unidentified fragment'] } };

const incomingGroups = [
  { id: 'north', title: 'Northern Lines', artistId: 'mira', releaseId: 'north', fileIds: [...Array.from({ length: 8 }, (_, i) => `north-f${i + 1}`), 'north-extra'], candidateId: 'p-north', mode: 'provider', autoEligible: false, reason: 'Неполный релиз; одна спорная композиция', processed: false, drafts: {} },
  { id: 'hours', title: 'Small Hours', artistId: 'arden', releaseId: 'hours', fileIds: Array.from({ length: 8 }, (_, i) => `hours-f${i + 1}`), candidateId: 'p-hours', mode: 'provider', autoEligible: true, reason: '8 / 8: все назначения уверенные', processed: false, drafts: {} },
  { id: 'mix', title: 'Road Notes — личный сборник', artistId: 'various', releaseId: 'mix', fileIds: Array.from({ length: 5 }, (_, i) => `mix-f${i + 1}`), candidateId: null, mode: 'local', autoEligible: false, reason: 'Релиза нет у провайдера; композиции из разных альбомов', processed: false, drafts: {} }
];

// Fictional browsing fixtures for approved catalog and inventory scenarios.
// They are deliberately separate from provider identity and do not imply full
// MusicBrainz discography coverage.
const absentProviderGroups = [
  { id: 'absent-blue', title: 'Blue Atlas', artistId: 'mira', groupId: 'demo-rg-blue', editions: [
    { id: 'absent-blue-a', year: '1998', country: 'GB', format: 'CD', disambiguation: 'Первое издание', titles: ['Glass Harbor', 'The Map', 'Blue Atlas'] },
    { id: 'absent-blue-b', year: '2001', country: 'JP', format: 'CD', disambiguation: 'Японское издание', titles: ['Glass Harbor', 'The Map', 'Blue Atlas', 'After Rain'] }
  ] }
];

// Location identities let root-removal previews distinguish a shared variant
// from one whose final location is about to disappear.
for (const file of Object.values(demoFiles)) {
  file.locations = [{ id: `loc-${file.id}`, rootId: 'root-backup', path: file.path }];
}
demoFiles['north-f1'].locations.push({ id: 'loc-north-f1-backup', rootId: 'root-backup', path: '/mnt/backup/Northern Lines/01 - First Light.flac' });
demoFiles['north-f1'].sha256 = 'demo-sha256-north-f1';
demoFiles['north-f1'].locations[0].rootId = 'root-inbox';
demoFiles['north-f3'].locations = [{ id: 'loc-north-f3-final', rootId: 'root-library', path: demoFiles['north-f3'].path }];

// A lossless alternative remains linked but is not selected or auto-fallbacked.
demoFiles['north-t1-mp3'] = { ...structuredClone(demoFiles['north-f1']), id: 'north-t1-mp3', name: '01 - First Light.mp3', path: '/mnt/music/First Light.mp3', codec: 'MP3', bits: null, size: '8.2 MB', locations: [{ id: 'loc-north-t1-mp3', rootId: 'root-library', path: '/mnt/music/First Light.mp3' }] };
const northTrackOne = localReleases.find(release => release.id === 'north').tracks[0];
northTrackOne.sourceIds.push('north-t1-mp3');
demoFiles['north-f3-mp3'] = { ...structuredClone(demoFiles['north-f3']), id: 'north-f3-mp3', name: '03 - Northbound.mp3', path: '/mnt/music/Northbound.mp3', codec: 'MP3', bits: null, size: '9.1 MB', locations: [{ id: 'loc-north-f3-mp3', rootId: 'root-backup', path: '/mnt/backup/Northbound.mp3' }] };
const northTrackThree = localReleases.find(release => release.id === 'north').tracks[2];
northTrackThree.sourceIds.push('north-f3-mp3');

// An explicit effective metadata change plus an independently observed file drift.
const northTrackTwo = localReleases.find(release => release.id === 'north').tracks[1];
northTrackTwo.title = 'Still Water (local edit)';
northTrackTwo.coverHash = 'demo-cover-north';
northTrackTwo.lyrics = null;
northTrackTwo.observedFile = { state: 'drift', mtime: 'изменён вне приложения', size: 'не совпадает' };
const protectedTrack = localReleases.find(release => release.id === 'rooms').tracks[0];
protectedTrack.manualProtected = { TITLE: true };
protectedTrack.providerTitle = 'Morning Room (provider correction)';
protectedTrack.coverHash = protectedTrack.published.coverHash = 'demo-cover-rooms';
protectedTrack.lyrics = null;
protectedTrack.metadataBaselineExtras = { coverHash: protectedTrack.published.coverHash, lyrics: protectedTrack.published.lyrics };

const sourceLessPublishedTrack = localReleases.find(release => release.id === 'rooms').tracks[7];
const sourceLessFileId = sourceLessPublishedTrack.sourceId;
sourceLessPublishedTrack.sourceId = null;
sourceLessPublishedTrack.sourceIds = [];
demoFiles[sourceLessFileId].locations = [];

// Second concrete edition of the same release group, independently materialized.
const japanProvider = providerReleases.find(release => release.id === 'p-japan');
const japanLocal = { ...structuredClone(japanProvider), id: 'north-japan-local', providerId: 'p-japan', releaseGroupId: 'demo-rg-north', tracks: japanProvider.tracks.map((track, index) => {
  const published = index === 0 ? { tags: structuredClone(providerTags(japanProvider, track)), coverHash: null, lyrics: null, codec: 'FLAC', container: 'MKA', sourceId: 'north-f1', readAt: '26.09.2026 11:02 · Japan edition', path: `/srv/music/Mira Vale/2022 - Northern Lines/${String(track.position).padStart(2, '0')} - ${track.title}.mka` } : null;
  return { ...track, id: `north-japan-local-t${index + 1}`, sourceId: index < 2 ? `north-f${index + 1}` : null, sourceIds: index < 2 ? [`north-f${index + 1}`] : [], published, ...(published ? { metadataBaseline: structuredClone(published.tags), metadataBaselineExtras: { coverHash: published.coverHash, lyrics: published.lyrics } } : {}) };
}) };
localReleases.push(japanLocal);
for (const release of localReleases.filter(item => item.id === 'north' || item.id === 'north-japan-local')) release.releaseGroupId = 'demo-rg-north';
for (const release of providerReleases.filter(item => item.id === 'p-north' || item.id === 'p-japan')) release.releaseGroupId = 'demo-rg-north';

const recoveryFixture = { trackId: 'north-t1', targetPath: '/srv/music/Mira Vale/2021 - Northern Lines/01 - First Light.mka', tagsMatch: true, technicalMatch: true, authorityCurrent: true, sourceIdentityProof: false };
