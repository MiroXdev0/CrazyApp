package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

func (c *cliClient) nodes() ([]NodeRecord, error) {
	var value []NodeRecord
	err := c.request(http.MethodGet, "/v1/nodes", nil, &value)
	return value, err
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

func (c *cliClient) submit(request jobRequest) (Job, error) {
	var value Job
	err := c.request(http.MethodPost, "/v1/jobs", request, &value)
	return value, err
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
		if job.Status == JobFailed {
			code := ""
			message := "Controller reported failure without details"
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
