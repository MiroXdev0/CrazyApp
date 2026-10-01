package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cliHealth struct {
	Status  string `json:"status"`
	Service string `json:"service"`
	Version string `json:"version"`
	Nodes   int    `json:"nodes"`
}

type cliClient struct {
	baseURL string
	http    *http.Client
}

type cliError struct {
	message string
}

func (e *cliError) Error() string { return e.message }

func newCLIClient() *cliClient {
	address := strings.TrimSpace(os.Getenv("NODREN_CONTROLLER_URL"))
	if address == "" {
		address = strings.TrimSpace(os.Getenv("NODREN_HTTP_ADDR"))
	}
	if address == "" {
		address = "127.0.0.1:8080"
	}
	address = strings.TrimRight(address, "/")
	if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
		address = "http://" + address
	}
	return &cliClient{
		baseURL: address,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *cliClient) request(method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return &cliError{message: "Invalid request: " + err.Error()}
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return &cliError{message: "Invalid Controller URL: " + err.Error()}
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return &cliError{message: fmt.Sprintf("Controller unreachable at %s: %v", c.baseURL, err)}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return &cliError{message: "Invalid Controller response: " + err.Error()}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = response.Status
		}
		return &cliError{message: fmt.Sprintf("Controller returned HTTP %d: %s", response.StatusCode, message)}
	}
	if output == nil || len(bytes.TrimSpace(responseBody)) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &cliError{message: "Invalid Controller response: " + err.Error()}
	}
	return nil
}

func (c *cliClient) health() (cliHealth, error) {
	var value cliHealth
	err := c.request(http.MethodGet, "/health", nil, &value)
	return value, err
}

func (c *cliClient) clusterStatus() (clusterStatusResponse, error) {
	var value clusterStatusResponse
	err := c.request(http.MethodGet, "/v1/cluster/status", nil, &value)
	return value, err
}

func (c *cliClient) nodes() ([]NodeRecord, error) {
	var value []NodeRecord
	err := c.request(http.MethodGet, "/v1/nodes", nil, &value)
	return value, err
}

func (c *cliClient) node(id string) (NodeRecord, error) {
	var value NodeRecord
	err := c.request(http.MethodGet, "/v1/nodes/"+url.PathEscape(id), nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown worker: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) workerStats(id string) (workerStatsResponse, error) {
	var value workerStatsResponse
	err := c.request(http.MethodGet, "/v1/nodes/"+url.PathEscape(id)+"/stats", nil, &value)
	return value, err
}

func (c *cliClient) workerAction(id, action string) (NodeRecord, error) {
	var value NodeRecord
	err := c.request(http.MethodPost, "/v1/nodes/"+url.PathEscape(id)+"/"+action, nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown worker: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) jobs() ([]Job, error) {
	var value []Job
	err := c.request(http.MethodGet, "/v1/jobs", nil, &value)
	return value, err
}

func (c *cliClient) job(id string) (Job, error) {
	var value Job
	err := c.request(http.MethodGet, "/v1/jobs/"+id, nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown job: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) jobStats(id string) (jobStatsResponse, error) {
	var value jobStatsResponse
	err := c.request(http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/stats", nil, &value)
	return value, err
}

func (c *cliClient) jobPartitions(id string) ([]Partition, error) {
	var value []Partition
	err := c.request(http.MethodGet, "/v1/jobs/"+url.PathEscape(id)+"/partitions", nil, &value)
	return value, err
}

func (c *cliClient) events() error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+"/v1/events", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 0}).Do(request)
	if err != nil {
		return &cliError{message: fmt.Sprintf("Controller unreachable at %s: %v", c.baseURL, err)}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &cliError{message: fmt.Sprintf("Controller returned HTTP %d", response.StatusCode)}
	}
	decoder := bufio.NewScanner(response.Body)
	for decoder.Scan() {
		line := strings.TrimSpace(decoder.Text())
		if strings.HasPrefix(line, "data:") {
			fmt.Println(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return decoder.Err()
}

func (c *cliClient) jobAction(id, action string) (Job, error) {
	var value Job
	err := c.request(http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/"+action, nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown job: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) updateDistribution(id string, request distributionRequest) (Job, error) {
	var value Job
	err := c.request(http.MethodPut, "/v1/jobs/"+url.PathEscape(id)+"/distribution", request, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown job: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) submit(request jobRequest) (Job, error) {
	var value Job
	err := c.request(http.MethodPost, "/v1/jobs", request, &value)
	return value, err
}

func (c *cliClient) tasks() ([]Job, error) {
	var value []Job
	err := c.request(http.MethodGet, "/v1/tasks", nil, &value)
	return value, err
}

func (c *cliClient) task(id string) (Job, error) {
	var value Job
	err := c.request(http.MethodGet, "/v1/tasks/"+url.PathEscape(id), nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown task: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) submitTask(request taskRequest) (Job, error) {
	var value Job
	err := c.request(http.MethodPost, "/v1/tasks", request, &value)
	return value, err
}

func (c *cliClient) taskAction(id, action string) (Job, error) {
	var value Job
	err := c.request(http.MethodPost, "/v1/tasks/"+url.PathEscape(id)+"/"+action, nil, &value)
	if err != nil {
		if cliErr, ok := err.(*cliError); ok && strings.Contains(cliErr.message, "HTTP 404") {
			return value, &cliError{message: "Unknown task: " + id}
		}
		return value, err
	}
	return value, nil
}

func (c *cliClient) taskResult(id string) (GeneralTaskResult, error) {
	var value GeneralTaskResult
	err := c.request(http.MethodGet, "/v1/tasks/"+url.PathEscape(id)+"/result", nil, &value)
	return value, err
}

func (c *cliClient) planAI(spec AIWorkloadSpec) (AIExecutionPlan, error) {
	var value AIExecutionPlan
	err := c.request(http.MethodPost, "/v1/ai/plans", spec, &value)
	return value, err
}

func (c *cliClient) aiPlan(id string) (AIExecutionPlan, error) {
	var value AIExecutionPlan
	err := c.request(http.MethodGet, "/v1/ai/plans/"+url.PathEscape(id), nil, &value)
	return value, err
}

func (c *cliClient) aiWorkers() ([]NodeRecord, error) {
	var value []NodeRecord
	err := c.request(http.MethodGet, "/v1/ai/workers", nil, &value)
	return value, err
}

func (c *cliClient) startAI(spec AIWorkloadSpec) (AIExecution, error) {
	var value AIExecution
	err := c.request(http.MethodPost, "/v1/ai/executions", spec, &value)
	return value, err
}

func (c *cliClient) aiExecution(id string) (AIExecution, error) {
	var value AIExecution
	err := c.request(http.MethodGet, "/v1/ai/executions/"+url.PathEscape(id), nil, &value)
	return value, err
}

func (c *cliClient) aiExecutionAction(id, action string) (AIExecution, error) {
	var value AIExecution
	err := c.request(http.MethodPost, "/v1/ai/executions/"+url.PathEscape(id)+"/"+action, nil, &value)
	return value, err
}

func (c *cliClient) uploadArtifact(path string) (TaskArtifact, error) {
	return c.uploadArtifactWithKind(path, "input")
}

func (c *cliClient) uploadArtifactWithKind(path, kind string) (TaskArtifact, error) {
	size, digest, err := hashArtifactFile(path)
	if err != nil {
		return TaskArtifact{}, &cliError{message: "Cannot inspect artifact: " + err.Error()}
	}
	if existing, found, lookupErr := c.lookupArtifact(digest, size); lookupErr != nil {
		return TaskArtifact{}, lookupErr
	} else if found {
		existing.Name = filepath.Base(path)
		existing.Kind = kind
		return existing, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return TaskArtifact{}, &cliError{message: "Cannot open artifact: " + err.Error()}
	}
	defer file.Close()
	request, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/artifacts?name="+url.QueryEscape(filepath.Base(path)), file)
	if err != nil {
		return TaskArtifact{}, &cliError{message: "Invalid Controller URL: " + err.Error()}
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Nodren-Artifact-Name", filepath.Base(path))
	request.Header.Set("X-Nodren-Artifact-Kind", kind)
	request.Header.Set("X-Nodren-Artifact-SHA256", digest)
	request.ContentLength = int64(size)
	response, err := c.http.Do(request)
	if err != nil {
		return TaskArtifact{}, &cliError{message: fmt.Sprintf("Controller unreachable at %s: %v", c.baseURL, err)}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(response.Body)
		return TaskArtifact{}, &cliError{message: fmt.Sprintf("Controller returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))}
	}
	var value TaskArtifact
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return TaskArtifact{}, &cliError{message: "Invalid artifact response: " + err.Error()}
	}
	return value, nil
}

func hashArtifactFile(path string) (uint64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return 0, "", err
	}
	return uint64(size), hex.EncodeToString(hasher.Sum(nil)), nil
}

func (c *cliClient) lookupArtifact(digest string, size uint64) (TaskArtifact, bool, error) {
	path := "/v1/artifacts?sha256=" + url.QueryEscape(digest) + "&size=" + strconv.FormatUint(size, 10)
	request, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return TaskArtifact{}, false, &cliError{message: "Invalid Controller URL: " + err.Error()}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return TaskArtifact{}, false, &cliError{message: fmt.Sprintf("Controller unreachable at %s: %v", c.baseURL, err)}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return TaskArtifact{}, false, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(response.Body)
		return TaskArtifact{}, false, &cliError{message: fmt.Sprintf("Controller returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))}
	}
	var value TaskArtifact
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return TaskArtifact{}, false, &cliError{message: "Invalid artifact lookup response: " + err.Error()}
	}
	return value, true, nil
}

func (c *cliClient) downloadArtifact(id, path string) error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+"/v1/artifacts/"+url.PathEscape(id), nil)
	if err != nil {
		return &cliError{message: "Invalid Controller URL: " + err.Error()}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return &cliError{message: fmt.Sprintf("Controller unreachable at %s: %v", c.baseURL, err)}
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(response.Body)
		return &cliError{message: fmt.Sprintf("Controller returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))}
	}
	file, err := os.Create(path)
	if err != nil {
		return &cliError{message: "Cannot create output file: " + err.Error()}
	}
	defer file.Close()
	if _, err := io.Copy(file, response.Body); err != nil {
		return &cliError{message: "Artifact download failed: " + err.Error()}
	}
	return nil
}

func (c *cliClient) waitForJob(id string, timeout time.Duration) (Job, error) {
	deadline := time.Now().Add(timeout)
	for {
		job, err := c.job(id)
		if err != nil {
			return Job{}, err
		}
		if job.Status == JobCompleted {
			return job, nil
		}
		if job.Status == JobFailed || job.Status == JobCancelled || job.Status == JobTimedOut {
			code := ""
			message := "Controller reported failure without details"
			if job.Status == JobCancelled {
				message = "job was cancelled"
			} else if job.Status == JobTimedOut {
				message = "job timed out"
			}
			if job.Result != nil {
				code = job.Result.ErrorCode
				if strings.TrimSpace(job.Result.Error) != "" {
					message = job.Result.Error
				}
			}
			return Job{}, &cliError{message: fmt.Sprintf("Job %s failed (%s): %s", id, code, message)}
		}
		if time.Now().After(deadline) {
			return Job{}, &cliError{message: fmt.Sprintf("Timed out waiting for job %s after %s", id, timeout)}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *cliClient) waitForTask(id string, timeout time.Duration) (Job, error) {
	deadline := time.Now().Add(timeout)
	for {
		task, err := c.task(id)
		if err != nil {
			return Job{}, err
		}
		if task.Status == JobCompleted {
			return task, nil
		}
		if task.Status == JobFailed || task.Status == JobCancelled || task.Status == JobTimedOut {
			message := "Controller reported task failure without details"
			if task.Status == JobCancelled {
				message = "task was cancelled"
			} else if task.Status == JobTimedOut {
				message = "task timed out"
			}
			if task.Execution != nil && strings.TrimSpace(task.Execution.Error) != "" {
				message = task.Execution.Error
			}
			return Job{}, &cliError{message: fmt.Sprintf("Task %s failed (%s): %s", id, task.Status, message)}
		}
		if time.Now().After(deadline) {
			return Job{}, &cliError{message: fmt.Sprintf("Timed out waiting for task %s after %s", id, timeout)}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
