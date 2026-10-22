`manual-lyrics-mp3.mka` is a synthetic 0.4-second, 44.1 kHz stereo sine-wave
encoded as MP3 and muxed in Matroska. It contains no user media. It was supplied
alongside its ffprobe JSON by the acceptance-test coordinator; that JSON is a
captured technical probe response for the same fixture, not a runtime ffprobe
execution. Recreate the audio with:

```sh
ffmpeg -f lavfi -i sine=frequency=440:sample_rate=44100:duration=0.4 -ac 2 -c:a libmp3lame -b:a 128k -f matroska manual-lyrics-mp3.mka
```
