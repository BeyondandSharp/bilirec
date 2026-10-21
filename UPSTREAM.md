# Upstream provenance

- Bilirec fork: **the repository root** (Go module root)
  - Upstream: https://github.com/bilirec/bilirec
  - Pinned tag: `v0.3.1`
  - Pinned commit: `26f4f8df343eacfcd1398e1cc06c4b2049718be4`
  - License: MIT (see `LICENSES/Bilirec-MIT.txt`; upstream notice preserved in NOTICE)
  - The upstream tree is overlaid at the root (`.git` removed) so the whole workspace is one git repo;
    our additions live in `rust/`, `web/`, `deploy/`, `scripts/`, `testdata/`, `docs/`.
  - To re-sync: re-clone upstream at the pinned commit, re-apply our patches, update this file.

- Reference sources (read-only, NOT distributed, gitignored under `refs/`):
  - https://github.com/BililiveRecorder/BililiveRecorder — GPL-3.0-only; FLV repair behavior reference.
  - https://github.com/biliup/biliup — MIT; streamlink/ffmpeg downloader reference.

- Frontend (vendored, source only): `web/`
  - Upstream: https://github.com/bilirec/bilirec-web
  - Pinned commit: `ab62df3f56ee05f03236bf83527b62bd487219ab`
  - License: MIT (see `web/LICENSE`)
  - Changes in this repo: downloader picker on the record-start form and
    downloader/repair/source-format badges on recording cards (T8).
  - Built with pnpm; `node_modules/` and `dist/` are gitignored.

- License of this repository: GPL-3.0-only (because `rust/crates/flv-repair` is a port of
  BililiveRecorder.Flv processing logic). MIT notices for Bilirec and bilirec-web are preserved
  in NOTICE.
