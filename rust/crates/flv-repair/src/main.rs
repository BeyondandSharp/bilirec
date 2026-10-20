// SPDX-License-Identifier: GPL-3.0-or-later
//
// `flvrepair` executable: FLV stream repair/analysis sidecar (docs/INTERFACES.md §1).

use std::process::ExitCode;

fn main() -> ExitCode {
    flv_repair::cli::entry()
}
