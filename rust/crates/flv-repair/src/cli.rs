// SPDX-License-Identifier: GPL-3.0-or-later
//
// `flvrepair` command line interface and process contract (docs/INTERFACES.md §1.1/§1.2,
// docs/FEATURES-V2.md §5.1/§5.3).

use crate::analyze;
use crate::group::GroupReader;
use crate::log::{EventLog, LogSink};
use crate::parser::{ParseError, TagReader};
use crate::pipeline::RepairPipeline;
use crate::toolbox::{self, ToolboxError};
use crate::writer::{FileSink, FlvWriter, Sink, StreamSink};
use std::ffi::OsString;
use std::fs::File;
use std::io::{self, BufReader, Read};
use std::path::{Path, PathBuf};
use std::process::ExitCode;

pub const EXIT_OK: u8 = 0;
pub const EXIT_IO: u8 = 1;
pub const EXIT_ARGS: u8 = 2;
pub const EXIT_UNRECOVERABLE: u8 = 3;

const DEFAULT_MAX_BUFFER: usize = 64 * 1024 * 1024;
const MIN_MAX_BUFFER: usize = 64 * 1024;
const MAX_MAX_BUFFER: usize = 4 * 1024 * 1024 * 1024;

const USAGE: &str = "usage: flvrepair <command> [options]
       flvrepair --version

commands:
  stream               repair FLV from stdin to stdout
  file                 repair FLV from --input to --output
  analyze              parse and report on --input
  export               export FLV tags to BililiveRecorder XML (.xml or .zip)
  danmaku-start-time   read start times from Bilibili danmaku XML files
  danmaku-merge        merge Bilibili danmaku XML files into one

repair options (stream, file):
  --mode <best-effort|strict>     repair mode (default: best-effort)
  --write-metadata <keep|drop>    keep script/onMetaData tags (default: keep)

file/analyze/export/toolbox options:
  --input <path>                  input file (file, analyze, export)
  --output <path>                 output file (file, export, danmaku-merge)
  --inputs <path> [<path> ...]    input files (danmaku-start-time, danmaku-merge)
  --offsets <ms,...>              per-input timestamp offsets in ms (danmaku-merge)
  --json                          JSON output (analyze, export, danmaku-start-time)

common options:
  --max-buffer <size>             internal buffer cap, e.g. 64MiB (default: 64MiB)
  -h, --help                      show this help";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Mode {
    BestEffort,
    Strict,
}

impl Mode {
    fn strict(self) -> bool {
        self == Mode::Strict
    }

    fn name(self) -> &'static str {
        match self {
            Mode::BestEffort => "best-effort",
            Mode::Strict => "strict",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Command {
    Stream,
    File,
    Analyze,
    Export,
    DanmakuStartTime,
    DanmakuMerge,
}

impl Command {
    fn parse(name: &str) -> Option<Command> {
        match name {
            "stream" => Some(Command::Stream),
            "file" => Some(Command::File),
            "analyze" => Some(Command::Analyze),
            "export" => Some(Command::Export),
            "danmaku-start-time" => Some(Command::DanmakuStartTime),
            "danmaku-merge" => Some(Command::DanmakuMerge),
            _ => None,
        }
    }
}

#[derive(Debug)]
struct Options {
    command: Command,
    mode: Mode,
    mode_set: bool,
    max_buffer: usize,
    input: Option<PathBuf>,
    output: Option<PathBuf>,
    inputs: Vec<PathBuf>,
    offsets: Option<Vec<i64>>,
    write_metadata: bool,
    write_metadata_set: bool,
    json: bool,
}

enum ParsedAction {
    Version,
    Help,
    Run(Options),
}

fn parse_args(args: &[OsString]) -> Result<ParsedAction, String> {
    let Some(first) = args.first() else {
        return Err("missing command".to_string());
    };
    let first = first.to_string_lossy();
    match first.as_ref() {
        "--version" | "-V" => return Ok(ParsedAction::Version),
        "--help" | "-h" | "help" => return Ok(ParsedAction::Help),
        _ => {}
    }
    let command = Command::parse(&first).ok_or_else(|| format!("unknown command: {first}"))?;
    let mut options = Options {
        command,
        mode: Mode::BestEffort,
        mode_set: false,
        max_buffer: DEFAULT_MAX_BUFFER,
        input: None,
        output: None,
        inputs: Vec::new(),
        offsets: None,
        write_metadata: true,
        write_metadata_set: false,
        json: false,
    };
    let mut index = 1;
    while index < args.len() {
        let argument = args[index].to_string_lossy().into_owned();
        index += 1;
        let (flag, inline_value) = match argument.split_once('=') {
            Some((flag, value)) => (flag.to_string(), Some(value.to_string())),
            None => (argument.clone(), None),
        };
        match flag.as_str() {
            "--mode" => {
                let value = take_value(&mut index, args, inline_value, "--mode")?;
                options.mode = match value.as_str() {
                    "best-effort" | "best_effort" | "besteffort" => Mode::BestEffort,
                    "strict" => Mode::Strict,
                    other => return Err(format!("invalid --mode value: {other}")),
                };
                options.mode_set = true;
            }
            "--max-buffer" => {
                let value = take_value(&mut index, args, inline_value, "--max-buffer")?;
                options.max_buffer = parse_size(&value)?;
            }
            "--input" => {
                let value = take_value(&mut index, args, inline_value, "--input")?;
                options.input = Some(PathBuf::from(value));
            }
            "--output" => {
                let value = take_value(&mut index, args, inline_value, "--output")?;
                options.output = Some(PathBuf::from(value));
            }
            "--inputs" => {
                let values = take_values(&mut index, args, inline_value, "--inputs")?;
                options.inputs.extend(values.into_iter().map(PathBuf::from));
            }
            "--offsets" => {
                let value = take_value(&mut index, args, inline_value, "--offsets")?;
                options.offsets = Some(parse_offsets(&value)?);
            }
            "--write-metadata" => {
                let value = take_value(&mut index, args, inline_value, "--write-metadata")?;
                options.write_metadata = match value.as_str() {
                    "keep" => true,
                    "drop" => false,
                    other => return Err(format!("invalid --write-metadata value: {other}")),
                };
                options.write_metadata_set = true;
            }
            "--json" => options.json = true,
            "--help" | "-h" => return Ok(ParsedAction::Help),
            other => return Err(format!("unknown argument: {other}")),
        }
    }
    validate_options(options).map(ParsedAction::Run)
}

fn take_value(
    index: &mut usize,
    args: &[OsString],
    inline_value: Option<String>,
    flag: &str,
) -> Result<String, String> {
    if let Some(value) = inline_value {
        return Ok(value);
    }
    let Some(value) = args.get(*index) else {
        return Err(format!("missing value for {flag}"));
    };
    *index += 1;
    Ok(value.to_string_lossy().into_owned())
}

/// Take one or more values for `--inputs`: either comma-separated after `=`, or all
/// following arguments until the next long option.
fn take_values(
    index: &mut usize,
    args: &[OsString],
    inline_value: Option<String>,
    flag: &str,
) -> Result<Vec<String>, String> {
    if let Some(value) = inline_value {
        let values: Vec<String> = value
            .split(',')
            .map(str::trim)
            .filter(|entry| !entry.is_empty())
            .map(str::to_string)
            .collect();
        if values.is_empty() {
            return Err(format!("empty value for {flag}"));
        }
        return Ok(values);
    }
    let start = *index;
    while *index < args.len() {
        let token = args[*index].to_string_lossy();
        if token.starts_with("--") || token == "-h" {
            break;
        }
        *index += 1;
    }
    if *index == start {
        return Err(format!("missing value for {flag}"));
    }
    Ok(args[start..*index]
        .iter()
        .map(|value| value.to_string_lossy().into_owned())
        .collect())
}

fn parse_offsets(raw: &str) -> Result<Vec<i64>, String> {
    let mut offsets = Vec::new();
    for entry in raw.split(',') {
        let entry = entry.trim();
        if entry.is_empty() {
            return Err(format!("invalid --offsets value: {raw}"));
        }
        offsets.push(
            entry
                .parse::<i64>()
                .map_err(|_| format!("invalid --offsets value: {raw}"))?,
        );
    }
    if offsets.is_empty() {
        return Err("empty --offsets value".to_string());
    }
    Ok(offsets)
}

fn validate_options(options: Options) -> Result<Options, String> {
    let repair_only = |options: &Options, flag: &str| -> Result<(), String> {
        if options.mode_set && !matches!(options.command, Command::Stream | Command::File) {
            return Err(format!("{flag} is only valid for stream and file"));
        }
        Ok(())
    };
    if options.write_metadata_set && !matches!(options.command, Command::Stream | Command::File) {
        return Err("--write-metadata is only valid for stream and file".to_string());
    }
    match options.command {
        Command::Stream => {
            repair_only(&options, "--mode")?;
            if options.input.is_some() || options.output.is_some() {
                return Err("stream does not accept --input/--output".to_string());
            }
            if !options.inputs.is_empty() || options.offsets.is_some() {
                return Err("stream does not accept --inputs/--offsets".to_string());
            }
            if options.json {
                return Err(
                    "--json is only valid for analyze, export and danmaku-start-time".to_string(),
                );
            }
        }
        Command::File => {
            repair_only(&options, "--mode")?;
            if options.input.is_none() {
                return Err("file requires --input".to_string());
            }
            if options.output.is_none() {
                return Err("file requires --output".to_string());
            }
            if !options.inputs.is_empty() || options.offsets.is_some() {
                return Err("file does not accept --inputs/--offsets".to_string());
            }
            if options.json {
                return Err(
                    "--json is only valid for analyze, export and danmaku-start-time".to_string(),
                );
            }
        }
        Command::Analyze => {
            repair_only(&options, "--mode")?;
            if options.input.is_none() {
                return Err("analyze requires --input".to_string());
            }
            if options.output.is_some() {
                return Err("analyze does not accept --output".to_string());
            }
            if !options.inputs.is_empty() || options.offsets.is_some() {
                return Err("analyze does not accept --inputs/--offsets".to_string());
            }
        }
        Command::Export => {
            repair_only(&options, "--mode")?;
            if options.input.is_none() {
                return Err("export requires --input".to_string());
            }
            if options.output.is_none() {
                return Err("export requires --output".to_string());
            }
            if !options.inputs.is_empty() || options.offsets.is_some() {
                return Err("export does not accept --inputs/--offsets".to_string());
            }
        }
        Command::DanmakuStartTime => {
            repair_only(&options, "--mode")?;
            if options.inputs.is_empty() {
                return Err("danmaku-start-time requires --inputs".to_string());
            }
            if options.input.is_some() || options.output.is_some() || options.offsets.is_some() {
                return Err("danmaku-start-time only accepts --inputs [--json]".to_string());
            }
        }
        Command::DanmakuMerge => {
            repair_only(&options, "--mode")?;
            if options.output.is_none() {
                return Err("danmaku-merge requires --output".to_string());
            }
            if options.inputs.len() < 2 {
                return Err("danmaku-merge requires at least 2 --inputs".to_string());
            }
            if options.input.is_some() {
                return Err("danmaku-merge does not accept --input".to_string());
            }
            if options.json {
                return Err("--json is not valid for danmaku-merge".to_string());
            }
            if let Some(offsets) = &options.offsets {
                if offsets.len() != options.inputs.len() {
                    return Err(format!(
                        "--offsets has {} entries but --inputs has {}",
                        offsets.len(),
                        options.inputs.len()
                    ));
                }
            }
        }
    }
    Ok(options)
}

fn parse_size(raw: &str) -> Result<usize, String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return Err("empty --max-buffer value".to_string());
    }
    let lower = trimmed.to_ascii_lowercase();
    let (number, multiplier) = if let Some(prefix) = lower.strip_suffix("mib") {
        (prefix, 1024u64 * 1024)
    } else if let Some(prefix) = lower.strip_suffix("mb") {
        (prefix, 1024u64 * 1024)
    } else if let Some(prefix) = lower.strip_suffix('m') {
        (prefix, 1024u64 * 1024)
    } else if let Some(prefix) = lower.strip_suffix("kib") {
        (prefix, 1024u64)
    } else if let Some(prefix) = lower.strip_suffix("kb") {
        (prefix, 1024u64)
    } else if let Some(prefix) = lower.strip_suffix('k') {
        (prefix, 1024u64)
    } else if let Some(prefix) = lower.strip_suffix("gib") {
        (prefix, 1024u64 * 1024 * 1024)
    } else if let Some(prefix) = lower.strip_suffix("gb") {
        (prefix, 1024u64 * 1024 * 1024)
    } else if let Some(prefix) = lower.strip_suffix('g') {
        (prefix, 1024u64 * 1024 * 1024)
    } else {
        // Contract default unit is MiB.
        (lower.as_str(), 1024u64 * 1024)
    };
    let value: u64 = number
        .trim()
        .parse()
        .map_err(|_| format!("invalid --max-buffer value: {raw}"))?;
    let bytes = value
        .checked_mul(multiplier)
        .ok_or_else(|| format!("--max-buffer value too large: {raw}"))?;
    let bytes = usize::try_from(bytes).map_err(|_| format!("--max-buffer too large: {raw}"))?;
    if !(MIN_MAX_BUFFER..=MAX_MAX_BUFFER).contains(&bytes) {
        return Err(format!(
            "--max-buffer must be between {} KiB and 4 GiB",
            MIN_MAX_BUFFER / 1024
        ));
    }
    Ok(bytes)
}

/// Entry point used by `main`.
pub fn entry() -> ExitCode {
    let args: Vec<OsString> = std::env::args_os().skip(1).collect();
    match parse_args(&args) {
        Ok(ParsedAction::Version) => {
            println!("flvrepair {}", env!("CARGO_PKG_VERSION"));
            ExitCode::SUCCESS
        }
        Ok(ParsedAction::Help) => {
            println!("{USAGE}");
            ExitCode::SUCCESS
        }
        Ok(ParsedAction::Run(options)) => run(options),
        Err(message) => {
            eprintln!("flvrepair: {message}");
            eprintln!("{USAGE}");
            ExitCode::from(EXIT_ARGS)
        }
    }
}

fn run(options: Options) -> ExitCode {
    signals::install();
    match options.command {
        Command::Stream => run_stream(&options),
        Command::File => run_file(&options),
        Command::Analyze => run_analyze(&options),
        Command::Export => {
            let (input, output) = match (options.input.as_deref(), options.output.as_deref()) {
                (Some(input), Some(output)) => (input, output),
                _ => return ExitCode::from(EXIT_ARGS),
            };
            run_toolbox(|| toolbox::export::run(input, output, options.json))
        }
        Command::DanmakuStartTime => {
            run_toolbox(|| toolbox::danmaku::run_start_time(&options.inputs, options.json))
        }
        Command::DanmakuMerge => {
            let output = match options.output.as_deref() {
                Some(output) => output,
                None => return ExitCode::from(EXIT_ARGS),
            };
            run_toolbox(|| {
                toolbox::danmaku::run_merge(output, &options.inputs, options.offsets.as_deref())
            })
        }
    }
}

fn run_toolbox<F: FnOnce() -> Result<(), ToolboxError>>(function: F) -> ExitCode {
    let mut log = EventLog::new();
    match function() {
        Ok(()) => ExitCode::from(EXIT_OK),
        Err(error) => {
            log.event("error", error.kind(), Some(error.message()));
            ExitCode::from(error.exit_code())
        }
    }
}

fn run_stream(options: &Options) -> ExitCode {
    let stdin = io::stdin();
    let reader = TagReader::new(stdin.lock(), options.max_buffer, options.mode.strict());
    let sink: Box<dyn Sink> = Box::new(StreamSink::new(io::stdout().lock()));
    let mut writer = FlvWriter::new(sink, options.max_buffer, options.write_metadata);
    let mut log = EventLog::new();
    log.event(
        "info",
        "start",
        Some(&format!(
            "mode={} write_metadata={}",
            options.mode.name(),
            if options.write_metadata {
                "keep"
            } else {
                "drop"
            }
        )),
    );
    let result = process_stream(reader, &mut writer, &mut log, options.mode.strict());
    finish_process(result, &mut log)
}

fn run_file(options: &Options) -> ExitCode {
    let input = match options.input.as_deref() {
        Some(input) => input,
        None => return ExitCode::from(EXIT_ARGS),
    };
    let output = match options.output.as_deref() {
        Some(output) => output,
        None => return ExitCode::from(EXIT_ARGS),
    };
    if same_file(input, output) {
        let mut log = EventLog::new();
        log.event(
            "error",
            "invalid_arguments",
            Some("--input and --output refer to the same file"),
        );
        return ExitCode::from(EXIT_ARGS);
    }
    let input_file = match File::open(input) {
        Ok(file) => file,
        Err(error) => {
            let mut log = EventLog::new();
            log.event(
                "error",
                "io_error",
                Some(&format!("cannot open {}: {error}", input.display())),
            );
            return ExitCode::from(EXIT_IO);
        }
    };
    let output_file = match File::create(output) {
        Ok(file) => file,
        Err(error) => {
            let mut log = EventLog::new();
            log.event(
                "error",
                "io_error",
                Some(&format!("cannot create {}: {error}", output.display())),
            );
            return ExitCode::from(EXIT_IO);
        }
    };
    let reader = TagReader::new(
        BufReader::with_capacity(64 * 1024, input_file),
        options.max_buffer,
        options.mode.strict(),
    );
    let sink: Box<dyn Sink> = Box::new(FileSink::new(output_file));
    let mut writer = FlvWriter::new(sink, options.max_buffer, options.write_metadata);
    let mut log = EventLog::new();
    log.event(
        "info",
        "start",
        Some(&format!(
            "mode={} write_metadata={} input={} output={}",
            options.mode.name(),
            if options.write_metadata {
                "keep"
            } else {
                "drop"
            },
            input.display(),
            output.display()
        )),
    );
    let result = process_stream(reader, &mut writer, &mut log, options.mode.strict());
    finish_process(result, &mut log)
}

fn run_analyze(options: &Options) -> ExitCode {
    let input = match options.input.as_deref() {
        Some(input) => input,
        None => return ExitCode::from(EXIT_ARGS),
    };
    let file = match File::open(input) {
        Ok(file) => file,
        Err(error) => {
            eprintln!("flvrepair: cannot open {}: {error}", input.display());
            return ExitCode::from(EXIT_IO);
        }
    };
    let reader = TagReader::new(
        BufReader::with_capacity(64 * 1024, file),
        options.max_buffer,
        false,
    );
    match analyze::analyze(reader) {
        Ok(report) => {
            if options.json {
                println!("{}", report.to_json());
            } else {
                print!("{}", report.to_human());
            }
            ExitCode::SUCCESS
        }
        Err(ParseError::Io(error)) => {
            eprintln!("flvrepair: I/O error: {error}");
            ExitCode::from(EXIT_IO)
        }
        Err(ParseError::Unrecoverable(message)) => {
            eprintln!("flvrepair: unrecoverable input: {message}");
            ExitCode::from(EXIT_UNRECOVERABLE)
        }
    }
}

fn process_stream<R: Read>(
    reader: TagReader<R>,
    writer: &mut FlvWriter,
    log: &mut EventLog,
    strict: bool,
) -> Result<(), ParseError> {
    let mut groups = GroupReader::new(reader);
    let mut pipeline = RepairPipeline::new();
    let mut flags_set = false;

    loop {
        if signals::interrupted() {
            log.event("info", "interrupted", Some("flushing and exiting"));
            break;
        }
        let Some(group) = groups.next_group()? else {
            break;
        };
        if !flags_set {
            pipeline.set_input_flags(groups.reader().header().flags);
            flags_set = true;
        }
        let actions = pipeline.process(group, log);
        for action in &actions {
            writer.write_action(action).map_err(ParseError::Io)?;
        }
    }

    if !flags_set {
        pipeline.set_input_flags(groups.reader().header().flags);
    }
    pipeline.add_parser_corruption(groups.reader().stats.corruption_events, log);
    let actions = pipeline.finish(log);
    for action in &actions {
        writer.write_action(action).map_err(ParseError::Io)?;
    }
    writer.finish().map_err(ParseError::Io)?;

    let parser_stats = groups.reader().stats;
    log_summary(log, &pipeline, writer, parser_stats);
    if strict && parser_stats.corruption_events > 0 {
        // Divergence per docs/PORTING.md §3.6: strict mode fails on corruption after
        // emitting all complete output produced so far.
        log.event(
            "error",
            "strict_corruption",
            Some("strict mode: input corruption or truncated tail detected"),
        );
        return Err(ParseError::Unrecoverable(
            "input corruption or truncated tail detected in strict mode",
        ));
    }
    Ok(())
}

fn log_summary(
    log: &mut EventLog,
    pipeline: &RepairPipeline,
    writer: &FlvWriter,
    parser: crate::parser::ParserStats,
) {
    let anomalies = pipeline
        .stats
        .anomalies()
        .iter()
        .map(|(class, count)| format!("\"{class}\":{count}"))
        .collect::<Vec<_>>()
        .join(",");
    let line = format!(
        "{{\"level\":\"info\",\"event\":\"summary\",\"bytes_in\":{},\"bytes_out\":{},\"tags_in\":{},\"tags_out\":{},\"anomalies\":{{{}}},\"dropped_leading_video\":{},\"metadata_synthesized\":{},\"truncated_bytes\":{},\"write_metadata\":\"{}\"}}",
        parser.bytes_read,
        writer.bytes_out(),
        pipeline.stats.tags_in,
        writer.stats.tags_out,
        anomalies,
        writer.stats.dropped_leading_video,
        writer.stats.metadata_synthesized,
        parser.truncated_bytes,
        if writer.write_metadata() { "keep" } else { "drop" },
    );
    log.raw_line(&line);
}

fn finish_process(result: Result<(), ParseError>, log: &mut EventLog) -> ExitCode {
    match result {
        Ok(()) => ExitCode::from(EXIT_OK),
        Err(ParseError::Io(error)) => {
            log.event("error", "io_error", Some(&error.to_string()));
            ExitCode::from(EXIT_IO)
        }
        Err(ParseError::Unrecoverable(message)) => {
            log.event("error", "unrecoverable_input", Some(message));
            ExitCode::from(EXIT_UNRECOVERABLE)
        }
    }
}

fn same_file(input: &Path, output: &Path) -> bool {
    if let Ok(input_canonical) = std::fs::canonicalize(input) {
        if let Ok(output_canonical) = std::fs::canonicalize(output) {
            return input_canonical == output_canonical;
        }
        return input_canonical == absolute_path(output);
    }
    input == output
}

fn absolute_path(path: &Path) -> PathBuf {
    if path.is_absolute() {
        path.to_path_buf()
    } else {
        match std::env::current_dir() {
            Ok(directory) => directory.join(path),
            Err(_) => path.to_path_buf(),
        }
    }
}

#[cfg(unix)]
mod signals {
    use std::sync::atomic::{AtomicBool, Ordering};

    static INTERRUPTED: AtomicBool = AtomicBool::new(false);

    extern "C" {
        fn signal(signum: i32, handler: extern "C" fn(i32)) -> isize;
    }

    extern "C" fn handle_signal(_signal: i32) {
        INTERRUPTED.store(true, Ordering::SeqCst);
    }

    /// Install SIGINT/SIGTERM handlers so buffered output can be flushed.
    pub fn install() {
        const SIGINT: i32 = 2;
        const SIGTERM: i32 = 15;
        // SAFETY: installing a plain function pointer handler; the handler only performs
        // an atomic store, which is async-signal-safe.
        unsafe {
            let _ = signal(SIGINT, handle_signal);
            let _ = signal(SIGTERM, handle_signal);
        }
    }

    pub fn interrupted() -> bool {
        INTERRUPTED.load(Ordering::SeqCst)
    }
}

#[cfg(not(unix))]
mod signals {
    pub fn install() {}
    pub fn interrupted() -> bool {
        false
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn args(values: &[&str]) -> Vec<OsString> {
        values.iter().map(OsString::from).collect()
    }

    fn run_options(values: &[&str]) -> Options {
        let parsed = parse_args(&args(values)).expect("parse");
        let ParsedAction::Run(options) = parsed else {
            panic!("expected run");
        };
        options
    }

    #[test]
    fn parses_stream_options() {
        let options = run_options(&["stream", "--mode", "strict", "--max-buffer", "16MiB"]);
        assert_eq!(options.command, Command::Stream);
        assert_eq!(options.mode, Mode::Strict);
        assert_eq!(options.max_buffer, 16 * 1024 * 1024);
        assert!(options.write_metadata);
    }

    #[test]
    fn parses_file_options_with_equals_syntax() {
        let options = run_options(&["file", "--input=a.flv", "--output", "b.flv"]);
        assert_eq!(options.input.as_deref(), Some(Path::new("a.flv")));
        assert_eq!(options.output.as_deref(), Some(Path::new("b.flv")));
    }

    #[test]
    fn parses_write_metadata_flag() {
        assert!(run_options(&["stream"]).write_metadata);
        assert!(run_options(&["stream", "--write-metadata", "keep"]).write_metadata);
        assert!(!run_options(&["stream", "--write-metadata", "drop"]).write_metadata);
        assert!(
            !run_options(&[
                "file",
                "--input",
                "a",
                "--output",
                "b",
                "--write-metadata=drop"
            ])
            .write_metadata
        );
    }

    #[test]
    fn parses_toolbox_commands() {
        let export = run_options(&["export", "--input", "a.flv", "--output", "a.xml", "--json"]);
        assert_eq!(export.command, Command::Export);
        assert!(export.json);

        let start = run_options(&["danmaku-start-time", "--inputs", "a.xml", "b.xml"]);
        assert_eq!(start.command, Command::DanmakuStartTime);
        assert_eq!(start.inputs.len(), 2);

        let merge = run_options(&[
            "danmaku-merge",
            "--output",
            "out.xml",
            "--inputs",
            "a.xml",
            "b.xml",
            "--offsets",
            "0,5000",
        ]);
        assert_eq!(merge.command, Command::DanmakuMerge);
        assert_eq!(merge.inputs.len(), 2);
        assert_eq!(merge.offsets.as_deref(), Some(&[0i64, 5000i64][..]));

        let merge_inline =
            run_options(&["danmaku-merge", "--output=out.xml", "--inputs=a.xml,b.xml"]);
        assert_eq!(merge_inline.inputs.len(), 2);
    }

    #[test]
    fn rejects_bad_arguments() {
        assert!(parse_args(&args(&[])).is_err());
        assert!(parse_args(&args(&["nonsense"])).is_err());
        assert!(parse_args(&args(&["stream", "--mode", "wrong"])).is_err());
        assert!(parse_args(&args(&["stream", "--max-buffer", "0"])).is_err());
        assert!(parse_args(&args(&["file", "--input", "a"])).is_err());
        assert!(parse_args(&args(&["analyze"])).is_err());
        assert!(parse_args(&args(&["stream", "--json"])).is_err());
        assert!(parse_args(&args(&["stream", "--write-metadata", "maybe"])).is_err());
        assert!(parse_args(&args(&[
            "analyze",
            "--input",
            "a",
            "--write-metadata",
            "drop"
        ]))
        .is_err());
        assert!(parse_args(&args(&["export", "--input", "a"])).is_err());
        assert!(parse_args(&args(&[
            "danmaku-merge",
            "--output",
            "o.xml",
            "--inputs",
            "a.xml"
        ]))
        .is_err());
        assert!(parse_args(&args(&[
            "danmaku-merge",
            "--output",
            "o.xml",
            "--inputs",
            "a",
            "b",
            "--offsets",
            "1"
        ]))
        .is_err());
        assert!(parse_args(&args(&["danmaku-start-time", "--inputs"])).is_err());
        assert!(parse_args(&args(&["analyze", "--input", "a", "--mode", "strict"])).is_err());
    }

    #[test]
    fn parses_offsets() {
        assert_eq!(parse_offsets("0,5000,-250").unwrap(), vec![0, 5000, -250]);
        assert!(parse_offsets("1,,2").is_err());
        assert!(parse_offsets("x").is_err());
    }

    #[test]
    fn parses_sizes() {
        assert_eq!(parse_size("64MiB").unwrap(), 64 * 1024 * 1024);
        assert_eq!(parse_size("64").unwrap(), 64 * 1024 * 1024);
        assert_eq!(parse_size("1024KiB").unwrap(), 1024 * 1024);
        assert_eq!(parse_size("1GiB").unwrap(), 1024 * 1024 * 1024);
        assert!(parse_size("nope").is_err());
        assert!(parse_size("1KiB").is_err());
    }

    #[test]
    fn version_and_help() {
        assert!(matches!(
            parse_args(&args(&["--version"])).unwrap(),
            ParsedAction::Version
        ));
        assert!(matches!(
            parse_args(&args(&["--help"])).unwrap(),
            ParsedAction::Help
        ));
    }
}
