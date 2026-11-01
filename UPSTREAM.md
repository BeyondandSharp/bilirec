# Upstream provenance

- Bilirec fork: **the repository root** (Go module root)
  - Upstream: https://github.com/bilirec/bilirec
  - Pinned tag: `v0.4.0`
  - Pinned commit: `7d51ba907361218825253407cfd59f2f6d537385`
  - `v0.4.0` was synced with a real Git merge (`git merge --no-ff v0.4.0`, 14 upstream
    commits since `v0.3.2`) so the upstream commits and their original authorship remain in
    the fork history; no cherry-pick / squash / rebase. Conflict resolution is recorded in
    `docs/DECISIONS.md` D022. The previous `v0.3.1` → `v0.3.2` sync used the same method (D021).
  - License: MIT (see `LICENSES/Bilirec-MIT.txt`; upstream notice preserved in NOTICE)
  - The upstream tree is overlaid at the root (`.git` removed) so the whole workspace is one git repo;
    our additions live in `rust/`, `web/`, `deploy/`, `scripts/`, `testdata/`, `docs/`.
  - The old root `Dockerfile` and `.github/` are intentionally not restored; this fork uses
    `deploy/` and `.forgejo/` instead. `.github/workflows/test.yaml` stays deleted during the
    `v0.4.0` merge (the upstream file is not brought back); CI lives in `.forgejo/`.
  - To re-sync: fetch upstream `main` and merge the desired release tag into `dev`;
    resolve conflicts in favor of the fork's deploy/CI layout, then update this file.

- Reference sources (read-only, NOT distributed, gitignored under `refs/`):
  - https://github.com/BililiveRecorder/BililiveRecorder — GPL-3.0-only; FLV repair behavior reference.
  - https://github.com/biliup/biliup — MIT; streamlink/ffmpeg downloader reference.

- Frontend (vendored, source only): `web/`
  - Upstream: https://github.com/bilirec/bilirec-web
  - Pinned commit: `6ad96b04b5c7af033d3c60698fa3bc8176f47728`
    (previously `ab62df3f56ee05f03236bf83527b62bd487219ab`)
  - License: MIT (see `web/LICENSE`)
  - Sync method: `web/` carries no Git history in this fork, so the move from `ab62df3` to
    `6ad96b0` (25 upstream commits) was ported via a temporary Git repository doing a
    three-way merge: pinned `ab62df3` tree → fork's vendored tree (local customizations) →
    `git merge` of the upstream `6ad96b0` tree, then copied back into `web/`
    (excluding upstream `.cursor/` rules). The temporary repository is not part of this repo.
  - Local customizations kept after the merge: downloader picker on the record-start form;
    downloader / repair / source-format badges on recording cards; batch FLV repair list and
    repair page; local Settings / Logs / Repair / Converts / Restart pages; three-language UI
    including a full `en` resource; list sort controls; and the recording date-range filter
    merged into the files view.
  - Upstream additions now included: preview/playback rewrite (FLV/TS/fMP4, DVR review,
    adjustable buffer), Document Picture-in-Picture, VOD VTT subtitle track with a draggable
    resizable overlay, file-page date range filter, optional danmaku-sidecar deletion when
    deleting a video, card badge layout, emote/danmaku track fixes. New dependencies:
    `flv.js`, `mpegts.js`; `react-day-picker` upgraded to v10.
  - Built with pnpm; `node_modules/` and `dist/` are gitignored.

- License of this repository: GPL-3.0-only (because `rust/crates/flv-repair` is a port of
  BililiveRecorder.Flv processing logic). MIT notices for Bilirec and bilirec-web are preserved
  in NOTICE.
