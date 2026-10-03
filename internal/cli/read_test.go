package cli_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/masonhuemmer/jkins/internal/cli"
	"github.com/masonhuemmer/jkins/internal/vault"
)

func readDeps(t *testing.T, url string) cli.Deps {
	t.Helper()
	state := filepath.Join(t.TempDir(), "jkins")
	if err := (vault.Store{Dir: state}).Save(vault.Credential{User: "alice", Token: "unique-secret-token"}); err != nil {
		t.Fatal(err)
	}
	return cli.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), StateDir: state, ControllerURL: url}
}

func runRead(t *testing.T, deps cli.Deps, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	deps.Out, deps.Err = &out, &errOut
	code := cli.Run(args, deps)
	return code, out.String(), errOut.String()
}

func TestNetworkCommandRequiresControllerURL(t *testing.T) {
	deps := readDepsWithToken(t, "", "unique-secret-token")
	code, out, stderr := runRead(t, deps, "job", "list", "--filter", "Build")
	if code == 0 || out != "" || !strings.Contains(stderr, "controller URL is not configured") {
		t.Fatalf("missing controller: code=%d out=%q err=%q", code, out, stderr)
	}
}

func TestJobListFiltersAndShowsFolderPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/json" || r.URL.Query().Get("tree") == "" {
			t.Errorf("request: %s", r.URL.String())
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "alice" || token != "unique-secret-token" {
			t.Errorf("auth: %q %q %v", user, token, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jobs":[{"name":"Build","_class":"hudson.model.FreeStyleProject","color":"blue"},{"name":"Deploy","_class":"hudson.model.FreeStyleProject","color":"red"}]}`))
	}))
	defer server.Close()
	code, out, stderr := runRead(t, readDeps(t, server.URL), "job", "list", "--filter", "Build")
	var jobs []struct {
		Path  string `json:"path"`
		Kind  string `json:"kind"`
		Color string `json:"color"`
	}
	if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &jobs) != nil || len(jobs) != 1 || jobs[0].Path != "Build" || jobs[0].Color != "blue" || jobs[0].Kind == "" {
		t.Fatalf("list: code=%d out=%q err=%q", code, out, stderr)
	}
}

func TestFolderListAndJobGetEncodeEachSegment(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.EscapedPath()] = true
		switch r.URL.EscapedPath() {
		case "/job/Team%20A/api/json":
			w.Write([]byte(`{"jobs":[{"name":"Compile #1","_class":"hudson.model.FreeStyleProject","color":"blue"}]}`))
		case "/job/Team%20A/job/Compile%20%231/api/json":
			w.Write([]byte(`{"name":"Compile #1","_class":"hudson.model.FreeStyleProject","color":"blue","lastBuild":{"number":42,"result":"SUCCESS","url":"https://ci.example/job/Team%20A/job/Compile%20%231/42/"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
		if r.URL.Query().Get("tree") == "" {
			t.Errorf("missing projection: %s", r.URL.String())
		}
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	code, out, stderr := runRead(t, deps, "job", "list", "--path", "Team A")
	if code != 0 || stderr != "" || !strings.Contains(out, `"path":"Team A/Compile #1"`) {
		t.Fatalf("folder list: %d %q %q", code, out, stderr)
	}
	code, out, stderr = runRead(t, deps, "job", "get", "Team A/Compile #1")
	if code != 0 || stderr != "" || !strings.Contains(out, `"status":"success"`) || !strings.Contains(out, `"number":42`) {
		t.Fatalf("job get: %d %q %q", code, out, stderr)
	}
	if !seen["/job/Team%20A/api/json"] || !seen["/job/Team%20A/job/Compile%20%231/api/json"] {
		t.Fatalf("paths: %#v", seen)
	}
}

func TestBuildGetAndLogUseExplicitBuildNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/job/Team%20A/job/Compile%20%231/42/api/json" {
			if r.URL.Query().Get("tree") == "" {
				t.Error("missing tree projection")
			}
			w.Write([]byte(`{"number":42,"result":"SUCCESS","timestamp":1700000000000,"duration":1234,"url":"https://ci.example/build/42/"}`))
		} else if r.URL.EscapedPath() == "/job/Team%20A/job/Compile%20%231/42/consoleText" {
			w.Write([]byte("build finished\n"))
		} else {
			t.Errorf("unexpected path %q", r.URL.EscapedPath())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	code, out, stderr := runRead(t, deps, "build", "get", "Team A/Compile #1", "42")
	if code != 0 || stderr != "" || !strings.Contains(out, `"number":42`) || !strings.Contains(out, `"timestamp":1700000000000`) || !strings.Contains(out, `"duration":1234`) {
		t.Fatalf("build get: %d %q %q", code, out, stderr)
	}
	code, out, stderr = runRead(t, deps, "build", "log", "Team A/Compile #1", "42")
	if code != 0 || stderr != "" || !strings.Contains(out, `"log":"build finished\n"`) {
		t.Fatalf("build log: %d %q %q", code, out, stderr)
	}
	code, out, stderr = runRead(t, deps, "--human", "build", "log", "Team A/Compile #1", "42")
	if code != 0 || out != "build finished\n" || stderr != "" {
		t.Fatalf("human log: %d %q %q", code, out, stderr)
	}
}

func TestReadErrorsAreClassifiedAndNeverShowServerBody(t *testing.T) {
	token := "unique-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/missing/api/json":
			http.Error(w, "missing "+token, http.StatusNotFound)
		case "/job/forbidden/api/json":
			http.Error(w, "forbidden "+token, http.StatusForbidden)
		case "/job/unavailable/api/json":
			http.Error(w, "unavailable "+token, http.StatusServiceUnavailable)
		case "/job/oversized/api/json":
			w.Write([]byte(strings.Repeat("x", 8<<20+1)))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	cases := []struct{ path, want string }{{"missing", "not found"}, {"forbidden", "permission"}, {"unavailable", "HTTP 503"}, {"oversized", "size limit"}}
	for _, tc := range cases {
		code, out, stderr := runRead(t, deps, "job", "get", tc.path)
		if code == 0 || out != "" || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, token) {
			t.Errorf("%s: %d %q %q", tc.path, code, out, stderr)
		}
	}
}

func TestBuildLogRedactsKnownTokenForms(t *testing.T) {
	token := "tok+en/secret"
	encoded := "tok%2Ben%2Fsecret"
	basic := "YWxpY2U6dG9rK2VuL3NlY3JldA=="
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("literal=" + token + " encoded=" + encoded + " Basic " + basic + " other text"))
	}))
	defer server.Close()
	deps := readDepsWithToken(t, server.URL, token)
	for _, args := range [][]string{{"build", "log", "Team/Job", "2"}, {"--human", "build", "log", "Team/Job", "2"}} {
		code, out, stderr := runRead(t, deps, args...)
		if code != 0 || stderr != "" || strings.Contains(out, token) || strings.Contains(out, encoded) || strings.Contains(out, basic) || !strings.Contains(out, "other text") {
			t.Errorf("%v: %d %q %q", args, code, out, stderr)
		}
	}
}

func readDepsWithToken(t *testing.T, url, token string) cli.Deps {
	t.Helper()
	state := filepath.Join(t.TempDir(), "jkins")
	if err := (vault.Store{Dir: state}).Save(vault.Credential{User: "alice", Token: token}); err != nil {
		t.Fatal(err)
	}
	return cli.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), StateDir: state, ControllerURL: url}
}

func TestTLSRequiresTrustAndRejectsMalformedCABundle(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"jobs":[]}`)) }))
	defer server.Close()
	deps := readDepsWithToken(t, "", "unique-secret-token")
	writeConfig := func(ca string) {
		t.Helper()
		data, _ := json.Marshal(map[string]string{"url": server.URL, "ca_file": ca})
		if err := os.WriteFile(deps.ConfigPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig("")
	if code, _, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code == 0 || stderr == "" {
		t.Fatalf("untrusted TLS: %d %q", code, stderr)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, cert, 0600); err != nil {
		t.Fatal(err)
	}
	writeConfig(caFile)
	if code, out, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code != 0 || out != "[]\n" || stderr != "" {
		t.Fatalf("trusted TLS: %d %q %q", code, out, stderr)
	}
	if err := os.WriteFile(caFile, append([]byte("garbage\n"), cert...), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code == 0 || !strings.Contains(stderr, "CA") {
		t.Fatalf("invalid CA prefix: %d %q", code, stderr)
	}
	if err := os.WriteFile(caFile, append(cert, []byte("garbage")...), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code == 0 || !strings.Contains(stderr, "CA") {
		t.Fatalf("invalid CA: %d %q", code, stderr)
	}
}

func expiredTLSServer(t *testing.T, hostname string, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(hostname); ip != nil {
		certificate.IPAddresses = []net.IP{ip}
	} else {
		certificate.DNSNames = []string{hostname}
	}
	raw, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{raw}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func TestSkipTLSVerifyIsExplicitAndUsesConfiguredController(t *testing.T) {
	requests := 0
	server := expiredTLSServer(t, "127.0.0.1", func(w http.ResponseWriter, r *http.Request) {
		requests++
		if user, token, ok := r.BasicAuth(); !ok || user != "alice" || token != "unique-secret-token" {
			t.Error("missing stored credential")
		}
		w.Write([]byte(`{"jobs":[]}`))
	})
	deps := readDepsWithToken(t, "", "unique-secret-token")
	setController := func(url string) {
		t.Helper()
		config, err := json.Marshal(map[string]string{"url": url})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
			t.Fatal(err)
		}
	}
	setController(server.URL)
	if code, _, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code == 0 || !strings.Contains(stderr, "certificate") || requests != 0 {
		t.Fatalf("expired certificate accepted by default: %d %q requests=%d", code, stderr, requests)
	}
	if code, out, stderr := runRead(t, deps, "--skip-tls-verify", "job", "list", "--filter", "A"); code != 0 || out != "[]\n" || stderr != "" || requests != 1 {
		t.Fatalf("skip verification read failed: %d %q %q requests=%d", code, out, stderr, requests)
	}
	mismatchRequests := 0
	mismatch := expiredTLSServer(t, "wrong.example", func(w http.ResponseWriter, r *http.Request) {
		mismatchRequests++
		w.Write([]byte(`{"jobs":[]}`))
	})
	setController(mismatch.URL)
	if code, _, stderr := runRead(t, deps, "job", "list", "--filter", "A"); code == 0 || !strings.Contains(stderr, "certificate") || mismatchRequests != 0 {
		t.Fatalf("wrong hostname accepted by default: %d %q requests=%d", code, stderr, mismatchRequests)
	}
	if code, out, stderr := runRead(t, deps, "job", "list", "--filter", "A", "--skip-tls-verify"); code != 0 || out != "[]\n" || stderr != "" || mismatchRequests != 1 {
		t.Fatalf("skip verification did not cover hostname: %d %q %q requests=%d", code, out, stderr, mismatchRequests)
	}
}

func TestSkipTLSVerifySupportsBuildQueueAgainstLocalController(t *testing.T) {
	posts := 0
	server := expiredTLSServer(t, "127.0.0.1", func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != http.MethodPost || r.URL.Path != "/job/Job/build" {
			t.Errorf("unexpected build request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
	})
	deps := readDepsWithToken(t, "", "unique-secret-token")
	config, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	if code, out, stderr := runRead(t, deps, "--skip-tls-verify", "build", "queue", "Job", "--execute"); code != 0 || !strings.Contains(out, `"queued":true`) || stderr != "" || posts != 1 {
		t.Fatalf("build queue with explicit TLS override: %d %q %q posts=%d", code, out, stderr, posts)
	}
}

func TestAuthenticatedReadRejectsTLSRedirect(t *testing.T) {
	redirected := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected++
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("authenticated read followed an HTTP redirect with Basic auth")
		}
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/login", http.StatusFound)
	}))
	defer secure.Close()

	deps := readDeps(t, secure.URL)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secure.Certificate().Raw})
	if err := os.WriteFile(caFile, cert, 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"url": secure.URL, "ca_file": caFile})
	if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runRead(t, deps, "job", "get", "Job")
	if code == 0 || out != "" || !strings.Contains(stderr, "HTTP 302") || redirected != 0 {
		t.Fatalf("redirect: code=%d out=%q err=%q follow requests=%d", code, out, stderr, redirected)
	}
}

func TestReadCommandsAreDiscoverableAndRejectMissingTargets(t *testing.T) {
	code, out, stderr := runRead(t, cli.Deps{}, "help")
	for _, command := range []string{"job list --filter", "job list --path", "job get", "build get", "build log"} {
		if code != 0 || stderr != "" || !strings.Contains(out, command) {
			t.Errorf("help missing %q: %d %q %q", command, code, out, stderr)
		}
	}
	deps := readDeps(t, "http://127.0.0.1:1")
	for _, args := range [][]string{{"job", "list"}, {"job", "get"}, {"build", "get", "Job"}, {"build", "log", "Job", "not-a-number"}, {"build", "get", "Folder//Job", "1"}} {
		code, out, stderr := runRead(t, deps, args...)
		if code == 0 || out != "" || stderr == "" {
			t.Errorf("accepted %v: %d %q %q", args, code, out, stderr)
		}
	}
}

func TestBuildLogLimitAndUnauthorizedRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/consoleText") {
			w.Write([]byte(strings.Repeat("x", (16<<20)+1)))
			return
		}
		http.Error(w, "unique-secret-token", http.StatusUnauthorized)
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	if code, out, stderr := runRead(t, deps, "build", "log", "Job", "1"); code == 0 || out != "" || !strings.Contains(stderr, "size limit") {
		t.Fatalf("oversized log: %d %q %q", code, out, stderr)
	}
	if code, out, stderr := runRead(t, deps, "build", "get", "Job", "1"); code == 0 || out != "" || !strings.Contains(stderr, "permission") || strings.Contains(stderr, "unique-secret-token") {
		t.Fatalf("unauthorized: %d %q %q", code, out, stderr)
	}
}
