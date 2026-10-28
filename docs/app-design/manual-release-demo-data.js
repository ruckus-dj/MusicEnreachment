window.manualReleaseDemoData = {
  artists: demoArtists.map(({ id, name }) => ({ id, name })),
  recordings: [...new Map(localReleases.flatMap(release => release.tracks
    .filter(track => track.recordingId)
    .map(track => [track.recordingId, {
      id: track.recordingId,
      title: track.title,
      artists: demoArtist(track.artistId)?.name || '',
      duration: `${Math.floor(track.duration / 60)}:${String(track.duration % 60).padStart(2, '0')}`
    }]))).values()],
  sources: Object.values(demoFiles).map(file => ({
    id: file.id,
    name: file.name,
    kind: `${file.codec} · ${file.size}`,
    path: file.path
  })),
  covers: localReleases.map(release => ({
    id: `cover-${release.id}`,
    title: `${release.title} · демо-обложка`,
    source: 'Иллюстративный вариант · не artwork-файл',
    mark: release.title[0] || 'M',
    style: release.color === 'forest' ? 'blue' : release.color === 'sand' ? 'gold' : ''
  }))
};
