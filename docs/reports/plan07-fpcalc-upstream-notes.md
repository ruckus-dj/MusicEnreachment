# План 07: технические сведения upstream fpcalc

Исследование 2026-10-05 для реализации; **не отчёт приёмки и не новый
продуктовый контракт**. Проверен source tag Chromaprint `v1.6.1`, а не
поведение всех бинарников на всех платформах. Реальное выполнение managed
tools и platform cases требуют отдельного доказательства.

## Invocation и provenance

- [fpcalc.cpp](https://github.com/acoustid/chromaprint/blob/v1.6.1/src/cmd/fpcalc.cpp)
  задаёт `g_max_duration = 120`. По решению владельца приложение не передаёт
  `-length`; не нужны также overrides алгоритма, chunking или ignore-errors.
- Фиксированный вызов `fpcalc -json -- <server-resolved absolute path>`
  получает один JSON object для одного файла. `--` поддерживается upstream.
  Не использовать shell, PATH или executable/path/version из HTTP body.
- `-version` возвращает фактическую Chromaprint version и FFmpeg library
  identifiers времени сборки. Это provenance fpcalc, не версия другого managed
  ffprobe и не runtime query версий FFmpeg libraries.
- `duration` в JSON — reported duration выбранного audio stream (fallback —
  container duration), не измеренная длительность обработанного фрагмента;
  неизвестная upstream duration выводится как `0.00`.
  Не объявлять это доказательством полного анализа или известной длительности.
- Ошибка decode/read может дать output, но nonzero exit. Такой частичный output
  не является успешным fingerprint. Stdout/stderr захватываются ограниченно,
  cancellation останавливает и reap-ит процесс; детали stderr не выдаются в safe
  error.

## Transport и границы гарантий

[FFmpegAudioReader](https://github.com/acoustid/chromaprint/blob/v1.6.1/src/audio/ffmpeg_audio_reader.h)
принимает filename/URL через `avformat_open_input`. CLI fpcalc не предоставляет
`-fd`, `-protocol_whitelist` или произвольный AVOption passthrough; `-` означает
non-seekable `pipe:0`. Не обещать descriptor transport, которого нет в этом CLI.

Server-resolved pathname с проверкой size/mtime соответствует существующему
решению владельца о доверенном использовании файловой системы
(`decisions.md`, 2026-10-04). Отсутствие descriptor transport само по себе не
является продуктовым blocker. Абсолютный путь **не блокирует вторичные локальные
или сетевые references из содержимого**, и этот вызов не является sandbox.
Не переносить guarantees ffprobe protocol whitelist на fpcalc.

## Формат fingerprint и test vectors

[Compressor](https://github.com/acoustid/chromaprint/blob/v1.6.1/src/fingerprint_compressor.cpp)
и [decompressor](https://github.com/acoustid/chromaprint/blob/v1.6.1/src/fingerprint_decompressor.cpp)
описывают URL-safe unpadded base64 и binary header:

- byte 0 — Chromaprint algorithm enum, **0-based**; default `TEST2` имеет enum 1,
  хотя CLI help называет его algorithm 2. Хранить namespace явно, не выдавать
  это за AcoustID numbering.
- bytes 1–3 — 24-bit big-endian число fingerprint words; успешный результат
  не должен быть пустым.
- Payload — LSB-first packed 3-bit differences с нулевым разделителем на word;
  значение 7 имеет дополнение в отдельном packed 5-bit блоке. Размеры блоков:
  `ceil(3*n/8)` и `ceil(5*exceptions/8)`.

Header validation — не полная валидация payload. Upstream decompressor допускает
trailing data; exact-length structural validation можно выполнить stdlib без
новой зависимости и без allocation по заявленному числу words.

В [upstream unit tests](https://github.com/acoustid/chromaprint/blob/v1.6.1/tests/test_fingerprint_decompressor.cpp)
есть synthetic compressed vector с algorithm enum 1 и 19 words:

```text
AQAAEwkjrUmSJQpUHflR9mjSJMdZpcO_Imdw9dCO9Clu4_wQPvhCB01w6xAtXNcAp5RASgDBhDSCGGIAcwA
```

Он проверяет parser, а не реальный audio fingerprint. Для реального fixture
upstream [test.mp3.fpcalc.out](https://github.com/acoustid/chromaprint/blob/v1.6.1/tests/data/test.mp3.fpcalc.out)
содержит raw golden; upstream CI сравнивает результат `fpcalc -raw` с этим файлом.
Audio fixture короче 120 секунд и не доказывает default truncation длинного
источника. Не смешивать synthetic parser evidence и real executable evidence.
