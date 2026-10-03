package jenkins

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/masonhuemmer/jkins/internal/config"
	"github.com/masonhuemmer/jkins/internal/vault"
)

const metadataLimit = 8 << 20
const logLimit = 16 << 20

var ErrNotFound = errors.New("not found")
var ErrUnauthorized = errors.New("authentication or permission denied")

type Client struct {
	base       *url.URL
	http       *http.Client
	credential vault.Credential
}

type Job struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Color string `json:"color"`
}

func New(cfg config.Config, credential vault.Credential, allowHTTP, skipTLSVerify bool) (*Client, error) {
	base, err := url.Parse(cfg.URL)
	if err != nil || base.Host == "" || (base.Scheme != "https" && !(allowHTTP && base.Scheme == "http")) {
		return nil, errors.New("invalid controller URL")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // Explicit per-invocation opt-in.
	}
	if cfg.CAFile != "" && !skipTLSVerify {
		caBytes, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, errors.New("cannot read CA file")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("cannot load system roots")
		}
		remaining := bytes.TrimSpace(caBytes)
		count := 0
		for len(remaining) > 0 {
			if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
				return nil, errors.New("CA file has malformed PEM certificates")
			}
			block, rest := pem.Decode(remaining)
			if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
				return nil, errors.New("CA file has malformed PEM certificates")
			}
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return nil, errors.New("CA file has malformed PEM certificates")
			}
			if !roots.AppendCertsFromPEM(pem.EncodeToMemory(block)) {
				return nil, errors.New("CA file has malformed PEM certificates")
			}
			count++
			remaining = bytes.TrimSpace(rest)
		}
		if count == 0 {
			return nil, errors.New("CA file has no valid PEM certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	return &Client{base: base, http: &http.Client{Timeout: 20 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, credential: credential}, nil
}

func (c *Client) redact(s string) string {
	token := c.credential.Token
	forms := []string{token, url.QueryEscape(token), url.PathEscape(token), base64.StdEncoding.EncodeToString([]byte(c.credential.User + ":" + token))}
	for _, form := range forms {
		if form != "" {
			s = strings.ReplaceAll(s, form, "[REDACTED]")
		}
	}
	return s
}

func (c *Client) get(path string, limit int64) ([]byte, error) {
	endpoint := *c.base
	parts := strings.SplitN(path, "?", 2)
	decoded, err := url.PathUnescape(parts[0])
	if err != nil {
		return nil, errors.New("invalid request path")
	}
	endpoint.Path = strings.TrimRight(c.base.Path, "/") + decoded
	endpoint.RawPath = strings.TrimRight(c.base.EscapedPath(), "/") + parts[0]
	if len(parts) == 2 {
		endpoint.RawQuery = parts[1]
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, errors.New("invalid request")
	}
	req.SetBasicAuth(c.credential.User, c.credential.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("controller request failed: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	default:
		return nil, fmt.Errorf("controller returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("controller response read failed")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("controller response exceeds size limit")
	}
	return data, nil
}

func (c *Client) ListJobs(filter string) ([]Job, error) {
	return c.list("", "", filter)
}

type BuildSummary struct {
	Number int    `json:"number"`
	Result string `json:"result"`
	URL    string `json:"url"`
}

type JobDetail struct {
	Job
	Status    string        `json:"status"`
	LastBuild *BuildSummary `json:"last_build"`
}

func JobPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("job path is required")
	}
	segments := strings.Split(path, "/")
	var escaped strings.Builder
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", errors.New("invalid job path")
		}
		escaped.WriteString("/job/")
		escaped.WriteString(url.PathEscape(segment))
	}
	return escaped.String(), nil
}

func (c *Client) ListFolder(path string) ([]Job, error) {
	prefix, err := JobPath(path)
	if err != nil {
		return nil, err
	}
	return c.list(prefix, path, "")
}

func (c *Client) list(endpoint, prefix, filter string) ([]Job, error) {
	data, err := c.get(endpoint+"/api/json?tree=jobs[name,_class,color]", metadataLimit)
	if err != nil {
		return nil, err
	}
	var response struct {
		Jobs []struct {
			Name  string `json:"name"`
			Class string `json:"_class"`
			Color string `json:"color"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, errors.New("invalid controller JSON")
	}
	jobs := make([]Job, 0, len(response.Jobs))
	for _, j := range response.Jobs {
		if !strings.Contains(j.Name, filter) {
			continue
		}
		name := j.Name
		if prefix != "" {
			name = prefix + "/" + name
		}
		jobs = append(jobs, Job{Path: c.redact(name), Kind: c.redact(j.Class), Color: c.redact(j.Color)})
	}
	return jobs, nil
}

func (c *Client) GetJob(path string) (JobDetail, error) {
	endpoint, err := JobPath(path)
	if err != nil {
		return JobDetail{}, err
	}
	data, err := c.get(endpoint+"/api/json?tree=name,_class,color,lastBuild[number,result,url]", metadataLimit)
	if err != nil {
		return JobDetail{}, err
	}
	var response struct {
		Name      string        `json:"name"`
		Class     string        `json:"_class"`
		Color     string        `json:"color"`
		LastBuild *BuildSummary `json:"lastBuild"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return JobDetail{}, errors.New("invalid controller JSON")
	}
	status := strings.TrimSuffix(response.Color, "_anime")
	switch status {
	case "blue":
		status = "success"
	case "red":
		status = "failure"
	case "yellow":
		status = "unstable"
	}
	if strings.HasSuffix(response.Color, "_anime") {
		status = "running"
	}
	if response.LastBuild != nil {
		response.LastBuild.Result = c.redact(response.LastBuild.Result)
		response.LastBuild.URL = c.redact(response.LastBuild.URL)
	}
	return JobDetail{Job: Job{Path: c.redact(path), Kind: c.redact(response.Class), Color: c.redact(response.Color)}, Status: c.redact(status), LastBuild: response.LastBuild}, nil
}

type Build struct {
	Number    int    `json:"number"`
	Result    string `json:"result"`
	Timestamp int64  `json:"timestamp"`
	Duration  int64  `json:"duration"`
	URL       string `json:"url"`
}

func (c *Client) GetBuild(path string, number int) (Build, error) {
	endpoint, err := JobPath(path)
	if err != nil {
		return Build{}, err
	}
	data, err := c.get(fmt.Sprintf("%s/%d/api/json?tree=number,result,timestamp,duration,url", endpoint, number), metadataLimit)
	if err != nil {
		return Build{}, err
	}
	var build Build
	if err := json.Unmarshal(data, &build); err != nil {
		return Build{}, errors.New("invalid controller JSON")
	}
	build.Result, build.URL = c.redact(build.Result), c.redact(build.URL)
	return build, nil
}

func (c *Client) BuildLog(path string, number int) (string, error) {
	endpoint, err := JobPath(path)
	if err != nil {
		return "", err
	}
	data, err := c.get(fmt.Sprintf("%s/%d/consoleText", endpoint, number), logLimit)
	if err != nil {
		return "", err
	}
	return c.redact(string(data)), nil
}

func (c *Client) QueueBuild(path string, parameters map[string]string) (string, error) {
	jobPath, err := JobPath(path)
	if err != nil {
		return "", err
	}
	endpoint := *c.base
	requestPath := jobPath + "/build"
	var body io.Reader
	if len(parameters) > 0 {
		requestPath = jobPath + "/buildWithParameters"
		values := url.Values{}
		for key, value := range parameters {
			values.Set(key, value)
		}
		body = strings.NewReader(values.Encode())
	}
	decoded, err := url.PathUnescape(requestPath)
	if err != nil {
		return "", errors.New("invalid request path")
	}
	endpoint.Path = strings.TrimRight(c.base.Path, "/") + decoded
	endpoint.RawPath = strings.TrimRight(c.base.EscapedPath(), "/") + requestPath
	endpoint.RawQuery = ""
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint.String(), body)
	if err != nil {
		return "", errors.New("invalid request")
	}
	req.SetBasicAuth(c.credential.User, c.credential.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("controller request failed: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusFound, http.StatusSeeOther:
	case http.StatusNotFound:
		return "", ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrUnauthorized
	default:
		return "", fmt.Errorf("controller returned HTTP %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	parsed, err := url.Parse(location)
	trusted := location != "" && err == nil && parsed.IsAbs() && parsed.User == nil && strings.EqualFold(parsed.Scheme, c.base.Scheme) && strings.EqualFold(parsed.Host, c.base.Host)
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther {
		queuePath := strings.TrimRight(c.base.Path, "/") + "/queue/item/"
		if !trusted || !strings.HasPrefix(parsed.Path, queuePath) {
			return "", fmt.Errorf("controller returned HTTP %d without a trusted queue item location", resp.StatusCode)
		}
		item := strings.TrimSuffix(strings.TrimPrefix(parsed.Path, queuePath), "/")
		if item == "" || strings.Trim(item, "0123456789") != "" {
			return "", fmt.Errorf("controller returned HTTP %d without a trusted queue item location", resp.StatusCode)
		}
	}
	if !trusted {
		return "", nil
	}
	return c.redact(location), nil
}
