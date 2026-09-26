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

function demoArtist(id) { return demoArtists.find(artist => artist.id === id); }

function providerTags(album, track) {
  const artist = demoArtist(track.artistId || album.artistId);
  return {
    TITLE: [track.title], ARTIST: [artist.name], ARTISTSORT: [artist.name],
    ALBUM: [album.title], ALBUMARTIST: [demoArtist(album.artistId).name],
    DATE: [album.year], COUNTRY: album.country ? [album.country] : [],
    LABEL: album.label ? [album.label] : [], CATALOGNUMBER: album.catalog ? [album.catalog] : [],
    TRACKNUMBER: [String(track.position)], DISCNUMBER: ['1'], GENRE: [artist.genre],
    MUSICBRAINZ_ARTISTID: [`demo-artist-${artist.id}`],
    MUSICBRAINZ_ALBUMID: album.providerId ? [album.providerId] : [],
    MUSICBRAINZ_TRACKID: track.recordingId ? [track.recordingId] : [],
    COMMENT: []
  };
}

for (const album of demoAlbums) {
  const tracks = album.titles.map((title, index) => {
    const artistId = album.id === 'mix' ? ['mira', 'forma', 'arden', 'mira', 'various'][index] : album.artistId;
    return { id: `${album.id}-t${index + 1}`, position: index + 1, title, duration: album.durations[index], artistId, recordingId: album.providerId ? `demo-rec-${album.id}-${index + 1}` : null, sourceId: null, providerTrackId: null, published: null };
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
    track.sourceId = fileId;
    track.providerTrackId = album.providerId ? `${album.providerId}-t${track.position}` : null;
    if (album.id === 'rooms' || album.id === 'signals' || (album.id === 'north' && track.position <= 5)) {
      const tags = structuredClone(raw);
      track.published = { tags, codec: album.id === 'north' ? 'MP3' : 'FLAC', container: 'MKA', sourceId: album.id === 'north' ? `previous-mp3-${fileId}` : fileId, readAt: '26.09.2026 10:18', path: `/srv/music/${demoArtist(album.artistId).name}/${album.year} - ${album.title}/${String(track.position).padStart(2, '0')} - ${track.title}.mka` };
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
  { id: 'mix', title: 'Road Notes — личный сборник', artistId: 'various', releaseId: 'mix', fileIds: Array.from({ length: 5 }, (_, i) => `mix-f${i + 1}`), candidateId: null, mode: 'local', autoEligible: false, reason: 'Релиза нет у провайдера; recordings из разных альбомов', processed: false, drafts: {} }
];
