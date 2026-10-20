# testdata conventions (owned by `port-spec` and `qa-harness`)

All fixtures are generated or synthetic; do not commit real broadcaster content.

## FLV fixtures (`testdata/flv/`, owner: port-spec)

- `fixtures/<name>.flv` — input sample.
- `fixtures/<name>.expect.json` — invariants the repaired output must satisfy
  (see `docs/INTERFACES.md` §1.4). No byte-exact goldens for repaired output.
- `gen/` — synthetic generators (Python stdlib preferred; ffmpeg allowed for base streams).
- Required corruption classes: R1..R8 from `docs/INTERFACES.md`.

Suggested generation: use ffmpeg to create a clean 10-30s H.264+AAC FLV, then a Python
mutator to inject each corruption class. Keep total repo size of fixtures < 20 MB.

## HLS fixtures (`testdata/hls/`, owner: qa-harness)

- `ts/playlist.m3u8` + `seg*.ts`
- `fmp4/playlist.m3u8` + `init.mp4` + `seg*.m4s`
- Playlists must be relative-URI so `scripts/origin.py` can serve them from any port.
