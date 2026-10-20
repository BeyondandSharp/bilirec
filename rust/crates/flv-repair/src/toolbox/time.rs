// SPDX-License-Identifier: GPL-3.0-or-later
//
// Date/time helpers shared by the toolbox commands. Dependency-free implementations of
// RFC 3339 parsing and of the .NET `DateTimeOffset` formats used by the reference
// toolbox XML exports.

use std::time::{SystemTime, UNIX_EPOCH};

/// Current wall clock time as Unix milliseconds.
pub fn now_unix_ms() -> i64 {
    system_time_to_unix_ms(SystemTime::now())
}

pub fn system_time_to_unix_ms(time: SystemTime) -> i64 {
    match time.duration_since(UNIX_EPOCH) {
        Ok(duration) => i64::try_from(duration.as_millis()).unwrap_or(i64::MAX),
        Err(error) => -i64::try_from(error.duration().as_millis()).unwrap_or(i64::MAX),
    }
}

/// Days since 1970-01-01 for a proleptic Gregorian civil date (Howard Hinnant's algorithm).
fn days_from_civil(year: i64, month: u32, day: u32) -> i64 {
    let year = if month <= 2 { year - 1 } else { year };
    let era = if year >= 0 { year } else { year - 399 } / 400;
    let year_of_era = year - era * 400;
    let month_prime = (i64::from(month) + 9) % 12;
    let day_of_year = (153 * month_prime + 2) / 5 + i64::from(day) - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    era * 146_097 + day_of_era - 719_468
}

/// Inverse of [`days_from_civil`].
fn civil_from_days(days: i64) -> (i64, u32, u32) {
    let days = days + 719_468;
    let era = if days >= 0 { days } else { days - 146_096 } / 146_097;
    let day_of_era = days - era * 146_097;
    let year_of_era =
        (day_of_era - day_of_era / 1460 + day_of_era / 36524 - day_of_era / 146_096) / 365;
    let year = year_of_era + era * 400;
    let day_of_year = day_of_era - (365 * year_of_era + year_of_era / 4 - year_of_era / 100);
    let month_prime = (5 * day_of_year + 2) / 153;
    let day = u32::try_from(day_of_year - (153 * month_prime + 2) / 5 + 1).unwrap_or(1);
    let month = u32::try_from(if month_prime < 10 {
        month_prime + 3
    } else {
        month_prime - 9
    })
    .unwrap_or(1);
    (if month <= 2 { year + 1 } else { year }, month, day)
}

fn split_unix_ms(unix_ms: i64) -> (i64, u32, u32, u32, u32, u32, u32) {
    let days = unix_ms.div_euclid(86_400_000);
    let millis_of_day = unix_ms.rem_euclid(86_400_000);
    let (year, month, day) = civil_from_days(days);
    let hour = u32::try_from(millis_of_day / 3_600_000).unwrap_or(0);
    let minute = u32::try_from((millis_of_day / 60_000) % 60).unwrap_or(0);
    let second = u32::try_from((millis_of_day / 1000) % 60).unwrap_or(0);
    let millis = u32::try_from(millis_of_day % 1000).unwrap_or(0);
    (year, month, day, hour, minute, second, millis)
}

/// Format a Unix millisecond timestamp like .NET `DateTimeOffset.ToString("o")` in UTC:
/// `2026-10-20T10:00:00.0000000+00:00`.
pub fn format_o(unix_ms: i64) -> String {
    let (year, month, day, hour, minute, second, millis) = split_unix_ms(unix_ms);
    format!("{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}.{millis:03}0000+00:00")
}

/// Format a timestamp with an explicit timezone offset (minutes east of UTC), using the
/// Newtonsoft.Json ISO date style (`2026-10-20T10:00:00+08:00`, fractional part omitted
/// when it is zero). Used for AMF0 Date values in the export XML.
pub fn format_datetime_offset(unix_ms: i64, timezone_minutes: i16) -> String {
    let offset = i64::from(timezone_minutes) * 60_000;
    let (year, month, day, hour, minute, second, millis) = split_unix_ms(unix_ms + offset);
    let sign = if timezone_minutes < 0 { '-' } else { '+' };
    let absolute = i64::from(timezone_minutes).abs();
    let fraction = if millis == 0 {
        String::new()
    } else {
        format!(".{millis:03}")
    };
    format!(
        "{year:04}-{month:02}-{day:02}T{hour:02}:{minute:02}:{second:02}{fraction}{sign}{:02}:{:02}",
        absolute / 60,
        absolute % 60
    )
}

/// Format a duration in milliseconds as seconds with three decimal places, as used by
/// the Bilibili danmaku XML `p`/`ts` attributes.
pub fn format_seconds_3(milliseconds: i64) -> String {
    format!("{:.3}", milliseconds as f64 / 1000.0)
}

/// Parse an RFC 3339 timestamp (with `T` or space separator, optional fractional
/// seconds, `Z`/`z` or `+HH:MM`/`-HH:MM`/`+HHMM` offsets) into Unix milliseconds.
pub fn parse_rfc3339_ms(input: &str) -> Option<i64> {
    let value = input.trim();
    let bytes = value.as_bytes();
    if bytes.len() < 19 {
        return None;
    }
    let year: i64 = value.get(0..4)?.parse().ok()?;
    if bytes.get(4)? != &b'-' || bytes.get(7)? != &b'-' {
        return None;
    }
    let month: u32 = value.get(5..7)?.parse().ok()?;
    let day: u32 = value.get(8..10)?.parse().ok()?;
    let separator = *bytes.get(10)?;
    if !matches!(separator, b'T' | b't' | b' ') {
        return None;
    }
    if bytes.get(13)? != &b':' || bytes.get(16)? != &b':' {
        return None;
    }
    let hour: u32 = value.get(11..13)?.parse().ok()?;
    let minute: u32 = value.get(14..16)?.parse().ok()?;
    let second: u32 = value.get(17..19)?.parse().ok()?;
    if !(1..=12).contains(&month)
        || !(1..=31).contains(&day)
        || hour > 23
        || minute > 59
        || second > 60
    {
        return None;
    }

    let mut remaining = &value[19..];
    let mut millis: i64 = 0;
    if let Some(fraction) = remaining.strip_prefix('.') {
        let digits = fraction
            .bytes()
            .take_while(|byte| byte.is_ascii_digit())
            .count();
        if digits == 0 {
            return None;
        }
        let fraction = &fraction[..digits];
        let mut padded = String::with_capacity(3);
        padded.push_str(fraction);
        while padded.len() < 3 {
            padded.push('0');
        }
        millis = padded.get(0..3)?.parse().ok()?;
        remaining = &remaining[1 + digits..];
    }

    let offset_minutes: i64 = if remaining.is_empty() {
        // Bare timestamps are treated as UTC (lenient; the recorder always writes an offset).
        0
    } else if remaining.eq_ignore_ascii_case("z") {
        0
    } else if let Some(rest) = remaining.strip_prefix('+') {
        parse_offset(rest)?
    } else {
        let rest = remaining.strip_prefix('-')?;
        parse_offset(rest)?.checked_neg()?
    };

    let days = days_from_civil(year, month, day);
    let seconds = i64::from(hour) * 3600 + i64::from(minute) * 60 + i64::from(second);
    Some(days * 86_400_000 + seconds * 1000 + millis - offset_minutes * 60_000)
}

fn parse_offset(value: &str) -> Option<i64> {
    let bytes = value.as_bytes();
    match bytes.len() {
        5 => {
            if bytes.get(2)? != &b':' {
                return None;
            }
            let hours: i64 = value.get(0..2)?.parse().ok()?;
            let minutes: i64 = value.get(3..5)?.parse().ok()?;
            Some(hours * 60 + minutes)
        }
        4 => {
            let hours: i64 = value.get(0..2)?.parse().ok()?;
            let minutes: i64 = value.get(2..4)?.parse().ok()?;
            Some(hours * 60 + minutes)
        }
        2 => value.parse().ok(),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_offsets_and_fractions() {
        assert_eq!(parse_rfc3339_ms("1970-01-01T00:00:00Z"), Some(0));
        assert_eq!(
            parse_rfc3339_ms("2026-10-20T10:00:00+08:00"),
            Some(1_792_461_600_000)
        );
        assert_eq!(
            parse_rfc3339_ms("2026-10-20T02:00:00.250+00:00"),
            Some(1_792_461_600_250)
        );
        assert_eq!(
            parse_rfc3339_ms("2026-10-20T02:00:00.25Z"),
            Some(1_792_461_600_250)
        );
        assert_eq!(
            parse_rfc3339_ms("2026-10-20T02:00:00+0000"),
            Some(1_792_461_600_000)
        );
        assert!(parse_rfc3339_ms("not a time").is_none());
        assert!(parse_rfc3339_ms("2026-13-20T00:00:00Z").is_none());
    }

    #[test]
    fn formats_dotnet_style() {
        assert_eq!(format_o(0), "1970-01-01T00:00:00.0000000+00:00");
        assert_eq!(
            parse_rfc3339_ms(&format_o(1_792_461_600_123)).unwrap(),
            1_792_461_600_123
        );
        assert_eq!(format_seconds_3(0), "0.000");
        assert_eq!(format_seconds_3(1500), "1.500");
        assert_eq!(format_datetime_offset(0, 480), "1970-01-01T08:00:00+08:00");
        assert_eq!(
            format_datetime_offset(250, 0),
            "1970-01-01T00:00:00.250+00:00"
        );
    }
}
