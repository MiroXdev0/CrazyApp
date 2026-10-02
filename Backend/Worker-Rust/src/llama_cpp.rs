use crate::protocol::GeneralTaskEnvelope;

use std::process::{Command, Stdio};

#[derive(Clone, Debug)]
pub struct LlamaRuntime {
    executable: String,
    version: String,
    gpu_backend: Option<String>,
}

impl LlamaRuntime {
    pub fn executable(&self) -> &str {
        &self.executable
    }

    pub fn version(&self) -> &str {
        &self.version
    }

    pub fn gpu_backend(&self) -> Option<&str> {
        self.gpu_backend.as_deref()
    }
}

fn command_output(executable: &str, argument: &str) -> Option<String> {
    let output = Command::new(executable)
        .arg(argument)
        .stdin(Stdio::null())
        .output()
        .ok()?;
    if !output.status.success() {
        return None;
    }
    let mut text = String::from_utf8_lossy(&output.stdout).into_owned();
    text.push_str(&String::from_utf8_lossy(&output.stderr));
    Some(text)
}

fn gpu_backend(text: &str) -> Option<String> {
    let lower = text.to_ascii_lowercase();
    if lower.contains("ggml-cuda") || lower.contains("cuda") {
        return Some("CUDA".to_string());
    }
    if lower.contains("hip") || lower.contains("rocm") {
        return Some("ROCm".to_string());
    }
    if lower.contains("vulkan") {
        return Some("Vulkan".to_string());
    }
    if lower.contains("metal") {
        return Some("Metal".to_string());
    }
    None
}

pub fn detect() -> Option<LlamaRuntime> {
    let configured = std::env::var("NODREN_LLAMA_CPP_EXECUTABLE").ok();
    let candidates = configured
        .into_iter()
        .chain([
            "llama-cli".to_string(),
            "llama.cpp".to_string(),
            "llama-cpp".to_string(),
        ])
        .collect::<Vec<_>>();
    for executable in candidates {
        let Some(version_output) = command_output(&executable, "--version") else {
            continue;
        };
        let help_output = command_output(&executable, "--help").unwrap_or_default();
        let combined = format!("{version_output}\n{help_output}");
        let lower = combined.to_ascii_lowercase();
        if !lower.contains("gpu-layers")
            && !lower.contains("n-gpu-layers")
            && !lower.contains("ngl")
        {
            return Some(LlamaRuntime {
                executable,
                version: first_line(&version_output),
                gpu_backend: None,
            });
        }
        return Some(LlamaRuntime {
            executable,
            version: first_line(&version_output),
            gpu_backend: gpu_backend(&combined),
        });
    }
    None
}

fn first_line(value: &str) -> String {
    value
        .lines()
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or("unknown")
        .chars()
        .take(256)
        .collect()
}

pub fn command_for_task(task: &GeneralTaskEnvelope) -> Result<Command, String> {
    let runtime = detect().ok_or_else(|| {
        "llama.cpp execution unavailable: no supported llama-cli executable was detected"
            .to_string()
    })?;
    let device = task
        .spec
        .environment
        .iter()
        .find(|(key, _)| key == "NODREN_AI_DEVICE")
        .map(|(_, value)| value.as_str())
        .unwrap_or("cpu");
    if device.eq_ignore_ascii_case("gpu") && runtime.gpu_backend().is_none() {
        return Err(format!(
            "llama.cpp GPU execution unavailable: {} does not report a CUDA/ROCm/Vulkan/Metal backend",
            runtime.executable()
        ));
    }
    let mut command = Command::new(runtime.executable());
    command.args(&task.spec.arguments);
    for (key, value) in &task.spec.environment {
        command.env(key, value);
    }
    if !task.spec.working_directory.is_empty() {
        command.current_dir(&task.spec.working_directory);
    }
    command
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    Ok(command)
}
