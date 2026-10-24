# Upstream provenance

- Bilirec fork: **the repository root** (Go module root)
  - Upstream: https://github.com/bilirec/bilirec
  - Pinned tag: `v0.3.2`
  - Pinned commit: `a14359f6e9d18eee37c31f856e8ad39e28c377e9`
  - The `v0.3.1` base was synced to `v0.3.2` with a real Git merge, so the upstream commits
    and their original authorship remain in the fork history.
  - License: MIT (see `LICENSES/Bilirec-MIT.txt`; upstream notice preserved in NOTICE)
  - The upstream tree is overlaid at the root (`.git` removed) so the whole workspace is one git repo;
    our additions live in `rust/`, `web/`, `deploy/`, `scripts/`, `testdata/`, `docs/`.
  - The old root `Dockerfile` and `.github/` are intentionally not restored; this fork uses
    `deploy/` and `.forgejo/` instead.
  - To re-sync: fetch upstream `main` and merge the desired release tag into `main`;
    resolve conflicts in favor of the fork's deploy/CI layout, then update this file.

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
