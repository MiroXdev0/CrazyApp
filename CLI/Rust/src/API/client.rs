use super::{ApiError, Health, Job, JobRequest, NodeRecord};

use serde::{Serialize, de::DeserializeOwned};
use std::{
    thread,
    time::{Duration, Instant},
};

#[derive(Clone)]
pub struct Client {
    base_url: String,
    agent: ureq::Agent,
}

impl Client {
    pub fn from_environment() -> Self {
        let configured = std::env::var("NODREN_CONTROLLER_URL")
            .or_else(|_| std::env::var("NODREN_HTTP_ADDR"))
            .unwrap_or_else(|_| "127.0.0.1:8080".to_string());
        Self::new(configured)
    }

    pub fn new(address: impl Into<String>) -> Self {
        let mut base_url = address.into().trim().trim_end_matches('/').to_string();
        if !base_url.starts_with("http://") && !base_url.starts_with("https://") {
            base_url = format!("http://{base_url}");
        }
        Self {
            base_url,
            agent: ureq::AgentBuilder::new()
                .timeout_connect(Duration::from_secs(2))
                .timeout_read(Duration::from_secs(10))
                .timeout_write(Duration::from_secs(10))
                .build(),
        }
    }

    pub fn base_url(&self) -> &str {
        &self.base_url
    }

    pub fn health(&self) -> Result<Health, ApiError> {
        self.get("/health")
    }

    pub fn nodes(&self) -> Result<Vec<NodeRecord>, ApiError> {
        self.get("/v1/nodes")
    }

    pub fn nodes_with_latency(&self) -> Result<(Vec<NodeRecord>, Duration), ApiError> {
        let started = Instant::now();
        let nodes = self.nodes()?;
        Ok((nodes, started.elapsed()))
    }

    pub fn node(&self, node_id: &str) -> Result<NodeRecord, ApiError> {
        self.nodes()?
            .into_iter()
            .find(|node| node.info.id == node_id)
            .ok_or_else(|| ApiError::UnknownNode(node_id.to_string()))
    }

    pub fn jobs(&self) -> Result<Vec<Job>, ApiError> {
        self.get("/v1/jobs")
    }

    pub fn job(&self, job_id: &str) -> Result<Job, ApiError> {
        let path = format!("/v1/jobs/{job_id}");
        match self.get(&path) {
            Err(ApiError::Http { status: 404, .. }) => {
                Err(ApiError::UnknownJob(job_id.to_string()))
            }
            result => result,
        }
    }

    pub fn submit_job(&self, request: &JobRequest) -> Result<Job, ApiError> {
        self.post("/v1/jobs", request)
    }

    pub fn wait_for_job(&self, job_id: &str, timeout: Duration) -> Result<Job, ApiError> {
        let started = Instant::now();
        loop {
            let job = self.job(job_id)?;
            if matches!(job.status.as_str(), "COMPLETED" | "FAILED") {
                if job.status == "FAILED" {
                    let result = job.result.as_ref();
                    return Err(ApiError::JobFailed {
                        job_id: job.id,
                        code: result
                            .map(|value| value.error_code.clone())
                            .unwrap_or_default(),
                        message: result
                            .map(|value| value.error.clone())
                            .filter(|value| !value.is_empty())
                            .unwrap_or_else(|| {
                                "Controller reported failure without details".to_string()
                            }),
                    });
                }
                return Ok(job);
            }
            if started.elapsed() >= timeout {
                return Err(ApiError::Timeout {
                    job_id: job_id.to_string(),
                    seconds: timeout.as_secs(),
                });
            }
            thread::sleep(Duration::from_millis(100));
        }
    }

    fn get<T: DeserializeOwned>(&self, path: &str) -> Result<T, ApiError> {
        let url = format!("{}{}", self.base_url, path);
        let response = self
            .agent
            .get(&url)
            .call()
            .map_err(|error| self.map_error(error))?;
        let body = response
            .into_string()
            .map_err(|error| ApiError::InvalidResponse(error.to_string()))?;
        serde_json::from_str(&body).map_err(|error| ApiError::InvalidResponse(error.to_string()))
    }

    fn post<T: Serialize, R: DeserializeOwned>(
        &self,
        path: &str,
        payload: &T,
    ) -> Result<R, ApiError> {
        let url = format!("{}{}", self.base_url, path);
        let response = self
            .agent
            .post(&url)
            .set("Content-Type", "application/json")
            .send_string(
                &serde_json::to_string(payload)
                    .map_err(|error| ApiError::InvalidResponse(error.to_string()))?,
            )
            .map_err(|error| self.map_error(error))?;
        let body = response
            .into_string()
            .map_err(|error| ApiError::InvalidResponse(error.to_string()))?;
        serde_json::from_str(&body).map_err(|error| ApiError::InvalidResponse(error.to_string()))
    }

    fn map_error(&self, error: ureq::Error) -> ApiError {
        match error {
            ureq::Error::Status(status, response) => {
                let message = response
                    .into_string()
                    .unwrap_or_else(|_| "request failed".to_string());
                ApiError::Http {
                    status,
                    message: message.trim().to_string(),
                }
            }
            ureq::Error::Transport(transport) => {
                let message = transport.to_string();
                if matches!(
                    transport.kind(),
                    ureq::ErrorKind::Dns | ureq::ErrorKind::ConnectionFailed
                ) {
                    ApiError::ControllerUnavailable {
                        url: self.base_url.clone(),
                        message,
                    }
                } else if message.to_ascii_lowercase().contains("timed out") {
                    ApiError::Timeout {
                        job_id: "request".to_string(),
                        seconds: 10,
                    }
                } else {
                    ApiError::ControllerUnavailable {
                        url: self.base_url.clone(),
                        message,
                    }
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_controller_addresses() {
        assert_eq!(
            Client::new("127.0.0.1:8080/").base_url(),
            "http://127.0.0.1:8080"
        );
        assert_eq!(
            Client::new("http://localhost:8080").base_url(),
            "http://localhost:8080"
        );
    }
}
