use std::{error::Error, fmt};

#[derive(Debug)]
pub enum ApiError {
    ControllerUnavailable {
        url: String,
        message: String,
    },
    Http {
        status: u16,
        message: String,
    },
    InvalidResponse(String),
    UnknownNode(String),
    UnknownJob(String),
    JobFailed {
        job_id: String,
        code: String,
        message: String,
    },
    Timeout {
        job_id: String,
        seconds: u64,
    },
}

impl fmt::Display for ApiError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ControllerUnavailable { url, message } => {
                write!(formatter, "Controller unreachable at {url}: {message}")
            }
            Self::Http { status, message } => {
                write!(formatter, "Controller returned HTTP {status}: {message}")
            }
            Self::InvalidResponse(message) => {
                write!(formatter, "Invalid Controller response: {message}")
            }
            Self::UnknownNode(node_id) => write!(formatter, "Unknown worker: {node_id}"),
            Self::UnknownJob(job_id) => write!(formatter, "Unknown job: {job_id}"),
            Self::JobFailed {
                job_id,
                code,
                message,
            } => {
                if code.is_empty() {
                    write!(formatter, "Job {job_id} failed: {message}")
                } else {
                    write!(formatter, "Job {job_id} failed ({code}): {message}")
                }
            }
            Self::Timeout { job_id, seconds } => {
                write!(
                    formatter,
                    "Timed out waiting for job {job_id} after {seconds}s"
                )
            }
        }
    }
}

impl Error for ApiError {}

#[cfg(test)]
mod tests {
    use super::ApiError;

    #[test]
    fn renders_actionable_job_failure() {
        let error = ApiError::JobFailed {
            job_id: "job-1".to_string(),
            code: "unsupported_workload".to_string(),
            message: "unsupported workload: future".to_string(),
        };
        assert_eq!(
            error.to_string(),
            "Job job-1 failed (unsupported_workload): unsupported workload: future"
        );
    }

    #[test]
    fn distinguishes_unknown_resources() {
        assert_eq!(
            ApiError::UnknownNode("N-1".into()).to_string(),
            "Unknown worker: N-1"
        );
        assert_eq!(
            ApiError::UnknownJob("J-1".into()).to_string(),
            "Unknown job: J-1"
        );
    }
}
