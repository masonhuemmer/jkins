package cli_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/masonhuemmer/jkins/internal/cli"
)

func TestBuildQueuePreviewShowsDeploymentTargetWithoutRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	code, out, stderr := runRead(t, deps, "build", "queue", "Deploy/PROD", "--param", "VERSION=1.2=release")
	var preview struct {
		JobPath    string            `json:"job_path"`
		Parameters map[string]string `json:"parameters"`
		Executed   bool              `json:"executed"`
	}
	if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &preview) != nil || preview.JobPath != "Deploy/PROD" || preview.Parameters["VERSION"] != "1.2=release" || preview.Executed || requests != 0 {
		t.Fatalf("preview: code=%d out=%q err=%q requests=%d", code, out, stderr, requests)
	}
}

func TestBuildQueueExecutePostsFormOnce(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, _ := io.ReadAll(r.Body)
		user, token, authenticated := r.BasicAuth()
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/job/Team%20A/job/Deploy%20%231/buildWithParameters" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !authenticated || user != "alice" || token != "unique-secret-token" {
			t.Errorf("queue request: %s %s", r.Method, r.URL.String())
		}
		if string(body) != "NOTE=a%3Db+%26%3F%2F&VERSION=1.2" {
			t.Errorf("body: %q", body)
		}
		w.Header().Set("Location", "http://"+r.Host+"/queue/item/42/")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	code, out, stderr := runRead(t, deps, "build", "queue", "Team A/Deploy #1", "--param", "VERSION=1.2", "--param", "NOTE=a=b &?/", "--execute")
	var queued struct {
		JobPath  string `json:"job_path"`
		Executed bool   `json:"executed"`
		Queued   bool   `json:"queued"`
		Location string `json:"location"`
	}
	if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &queued) != nil || !queued.Executed || !queued.Queued || queued.JobPath != "Team A/Deploy #1" || queued.Location != server.URL+"/queue/item/42/" || requests != 1 {
		t.Fatalf("queue: code=%d out=%q err=%q requests=%d", code, out, stderr, requests)
	}
}

func TestBuildQueueRedirectDoesNotSendSecondRequest(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			posts, follows := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/job/Deploy/build" {
					posts++
					body, _ := io.ReadAll(r.Body)
					if r.Method != http.MethodPost || r.URL.RawQuery != "" || len(body) != 0 {
						t.Errorf("queue request: %s %s", r.Method, r.URL.String())
					}
					w.Header().Set("Location", "/queue/item/42/")
					w.WriteHeader(status)
					return
				}
				follows++
			}))
			defer server.Close()
			code, out, stderr := runRead(t, readDeps(t, server.URL), "build", "queue", "Deploy", "--execute")
			if status == http.StatusFound && (code == 0 || out != "" || !strings.Contains(stderr, "HTTP 302")) {
				t.Errorf("302 result: code=%d out=%q err=%q", code, out, stderr)
			}
			if status == http.StatusTemporaryRedirect && (code == 0 || !strings.Contains(stderr, "HTTP 307")) {
				t.Errorf("307 result: code=%d out=%q err=%q", code, out, stderr)
			}
			if posts != 1 || follows != 0 {
				t.Fatalf("redirect: posts=%d follows=%d", posts, follows)
			}
		})
	}
}

func TestBuildQueueRedirectRequiresTrustedQueueItem(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		location string
		queued   bool
	}{
		{"302 queue item", http.StatusFound, "queue", true},
		{"303 queue item", http.StatusSeeOther, "queue", true},
		{"302 login", http.StatusFound, "login", false},
		{"303 error", http.StatusSeeOther, "error", false},
		{"302 missing location", http.StatusFound, "", false},
		{"303 other origin", http.StatusSeeOther, "https://other.example/queue/item/42/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts, follows := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/job/Deploy/build" {
					follows++
					return
				}
				posts++
				location := tc.location
				if location == "queue" {
					location = "http://" + r.Host + "/queue/item/42/"
				}
				if location == "login" || location == "error" {
					location = "http://" + r.Host + "/" + location
				}
				if location != "" {
					w.Header().Set("Location", location)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			code, out, stderr := runRead(t, readDeps(t, server.URL), "build", "queue", "Deploy", "--execute")
			if tc.queued {
				if code != 0 || stderr != "" || !strings.Contains(out, `"queued":true`) || !strings.Contains(out, server.URL+"/queue/item/42/") {
					t.Errorf("trusted redirect: code=%d out=%q err=%q", code, out, stderr)
				}
			} else if code == 0 || out != "" || !strings.Contains(stderr, "HTTP "+strconv.Itoa(tc.status)) {
				t.Errorf("untrusted redirect: code=%d out=%q err=%q", code, out, stderr)
			}
			if posts != 1 || follows != 0 {
				t.Fatalf("requests: posts=%d follows=%d", posts, follows)
			}
		})
	}
}

func TestBuildQueueAcceptedResponsesStillReportQueued(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusAccepted} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			code, out, stderr := runRead(t, readDeps(t, server.URL), "build", "queue", "Deploy", "--execute")
			if code != 0 || stderr != "" || !strings.Contains(out, `"queued":true`) {
				t.Fatalf("accepted response: code=%d out=%q err=%q", code, out, stderr)
			}
		})
	}
}

func TestBuildQueuePreviewNeedsNoCredentialAndRejectsInvalidArguments(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer server.Close()
	deps := cli.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), StateDir: filepath.Join(t.TempDir(), "empty"), ControllerURL: server.URL}
	code, out, stderr := runRead(t, deps, "build", "queue", "Deploy/PROD", "--param", "ENV=prod")
	if code != 0 || stderr != "" || !strings.Contains(out, `"job_path":"Deploy/PROD"`) || requests != 0 {
		t.Fatalf("credential-free preview: %d %q %q requests=%d", code, out, stderr, requests)
	}
	for _, args := range [][]string{
		{"build", "queue"}, {"build", "queue", ""}, {"build", "queue", "--execute"}, {"build", "queue", "Folder//Job"},
		{"build", "queue", "Job", "--param", "=value"}, {"build", "queue", "Job", "--param", "BAD KEY=value"},
		{"build", "queue", "Job", "--param", "NO_EQUALS"}, {"build", "queue", "Job", "--param"},
		{"build", "queue", "Job", "--unknown"}, {"build", "queue", "Job", "--execute", "--execute"},
	} {
		code, out, stderr := runRead(t, deps, args...)
		if code == 0 || out != "" || stderr == "" || requests != 0 {
			t.Errorf("accepted %v: %d %q %q requests=%d", args, code, out, stderr, requests)
		}
	}
	code, out, stderr = runRead(t, deps, "build", "queue", "Job", "--execute")
	if code == 0 || out != "" || !strings.Contains(stderr, "no credential") || requests != 0 {
		t.Fatalf("unauthenticated execute: %d %q %q requests=%d", code, out, stderr, requests)
	}
}

func TestBuildQueueOmitsUntrustedLocations(t *testing.T) {
	for _, location := range []string{"/queue/item/3/", "https://other.example/queue/item/3/", "//other.example/queue/item/3/"} {
		t.Run(location, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusCreated)
			}))
			defer server.Close()
			code, out, stderr := runRead(t, readDeps(t, server.URL), "build", "queue", "Job", "--execute")
			if code != 0 || stderr != "" || !strings.Contains(out, `"queued":true`) || strings.Contains(out, `"location"`) || strings.Contains(out, location) {
				t.Fatalf("location %q: %d %q %q", location, code, out, stderr)
			}
		})
	}
}

func TestBuildQueueFailureHidesServerBodyAndToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "queue rejected unique-secret-token", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	code, out, stderr := runRead(t, readDeps(t, server.URL), "build", "queue", "Job", "--execute")
	if code == 0 || out != "" || !strings.Contains(stderr, "HTTP 503") || strings.Contains(stderr, "unique-secret-token") || strings.Contains(stderr, "queue rejected") {
		t.Fatalf("failure: %d %q %q", code, out, stderr)
	}
}

func TestBuildQueueHelpExplainsExecutionGate(t *testing.T) {
	code, out, stderr := runRead(t, cli.Deps{}, "help")
	for _, phrase := range []string{"build queue JOB", "--param KEY=VALUE", "--execute", "preview", "deployment", "explicit target"} {
		if code != 0 || stderr != "" || !strings.Contains(strings.ToLower(out), strings.ToLower(phrase)) {
			t.Errorf("help missing %q: %d %q %q", phrase, code, out, stderr)
		}
	}
}
