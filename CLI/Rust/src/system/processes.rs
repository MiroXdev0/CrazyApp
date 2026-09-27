use std::process::Command;

pub fn count_nodren_processes() -> usize {
    #[cfg(target_os = "windows")]
    {
        count_windows()
    }

    #[cfg(not(target_os = "windows"))]
    {
        count_unix()
    }
}

#[cfg(target_os = "windows")]
fn count_windows() -> usize {
    let output = Command::new("tasklist")
        .output();

    let Ok(output) = output else {
        return 0;
    };

    let text = String::from_utf8_lossy(&output.stdout);

    text.lines()
        .filter(|line| {
            line.to_lowercase().contains("nodren")
        })
        .count()
}

#[cfg(not(target_os = "windows"))]
fn count_unix() -> usize {
    let output = Command::new("ps")
        .args(["-A", "-o", "comm="])
        .output();

    let Ok(output) = output else {
        return 0;
    };

    let text = String::from_utf8_lossy(&output.stdout);

    text.lines()
        .filter(|line| {
            line.to_lowercase().contains("nodren")
        })
        .count()
}