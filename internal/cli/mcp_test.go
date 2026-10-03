package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masonhuemmer/jkins/internal/cli"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connectMCP(t *testing.T, deps cli.Deps) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := cli.NewMCPServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "jkins-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestMCPServeCommandIsDocumentedAndRejectsExtraOperands(t *testing.T) {
	code, help, stderr := runRead(t, cli.Deps{}, "help")
	if code != 0 || stderr != "" || !strings.Contains(help, "jkins mcp serve") || !strings.Contains(help, "write_opt_in") {
		t.Fatalf("help: %d %q %q", code, help, stderr)
	}
	var stdout, errors bytes.Buffer
	code = cli.Run([]string{"mcp", "serve", "extra"}, cli.Deps{Out: &stdout, Err: &errors})
	if code == 0 || stdout.Len() != 0 || !strings.Contains(errors.String(), "invalid command") {
		t.Fatalf("extra operands: %d %q %q", code, stdout.String(), errors.String())
	}
}

func TestMCPQueueRequiresWriteOptIn(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/job/Deploy/job/PROD/buildWithParameters" || r.URL.RawQuery != "" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("VERSION") != "1=2 &?" {
			t.Errorf("form: %v, %v", r.Form, err)
		}
		w.Header().Set("Location", "http://"+r.Host+"/queue/item/8/")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	session := connectMCP(t, deps)
	args := map[string]any{"job_path": "Deploy/PROD", "parameters": map[string]string{"VERSION": "1=2 &?"}}
	preview := callMCP(t, session, "jkins_build_queue", args)
	var previewValue struct {
		JobPath  string `json:"job_path"`
		Executed bool   `json:"executed"`
	}
	if preview.IsError || json.Unmarshal([]byte(mcpText(t, preview)), &previewValue) != nil || previewValue.JobPath != "Deploy/PROD" || previewValue.Executed || posts != 0 {
		t.Fatalf("preview: %#v, posts=%d", preview, posts)
	}
	args["write_opt_in"] = true
	queued := callMCP(t, session, "jkins_build_queue", args)
	var queueValue struct {
		Executed bool   `json:"executed"`
		Queued   bool   `json:"queued"`
		Location string `json:"location"`
	}
	if queued.IsError || json.Unmarshal([]byte(mcpText(t, queued)), &queueValue) != nil || !queueValue.Executed || !queueValue.Queued || queueValue.Location != server.URL+"/queue/item/8/" || posts != 1 {
		t.Fatalf("queue: %#v, posts=%d", queued, posts)
	}
}

func TestMCPFailureHidesServerTextAndKnownToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rejected unique-secret-token", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	session := connectMCP(t, readDeps(t, server.URL))
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"jkins_read", map[string]any{"operation": "job_get", "path": "Deploy"}},
		{"jkins_build_queue", map[string]any{"job_path": "Deploy", "write_opt_in": true}},
	} {
		response := callMCP(t, session, call.name, call.args)
		message := mcpText(t, response)
		if !response.IsError || !strings.Contains(message, "HTTP 503") || strings.Contains(message, "unique-secret-token") || strings.Contains(message, "rejected") {
			t.Errorf("%s: %#v", call.name, response)
		}
	}
}

func TestMCPStatusAndLogNeverReturnStoredToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("log contains unique-secret-token\n"))
	}))
	defer server.Close()
	session := connectMCP(t, readDeps(t, server.URL))
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"jkins_status", map[string]any{}},
		{"jkins_read", map[string]any{"operation": "build_log", "path": "Deploy", "number": 7}},
	} {
		response := callMCP(t, session, call.name, call.args)
		message := mcpText(t, response)
		if response.IsError || strings.Contains(message, "unique-secret-token") || !strings.Contains(message, "alice") && call.name == "jkins_status" || !strings.Contains(message, "[REDACTED]") && call.name == "jkins_read" {
			t.Errorf("%s: %#v", call.name, response)
		}
	}
}

func TestMCPReadMatchesCLIResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/json":
			w.Write([]byte(`{"jobs":[{"name":"Deploy","_class":"hudson.model.FreeStyleProject","color":"blue"}]}`))
		case "/job/Deploy/api/json":
			w.Write([]byte(`{"name":"Deploy","_class":"hudson.model.FreeStyleProject","color":"blue","lastBuild":{"number":7,"result":"SUCCESS","url":"https://ci.example/job/Deploy/7/"}}`))
		case "/job/Deploy/7/api/json":
			w.Write([]byte(`{"number":7,"result":"SUCCESS","timestamp":1000,"duration":40,"url":"https://ci.example/job/Deploy/7/"}`))
		case "/job/Deploy/7/consoleText":
			w.Write([]byte("build complete\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	deps := readDeps(t, server.URL)
	session := connectMCP(t, deps)
	for _, test := range []struct {
		name string
		args map[string]any
		cli  []string
	}{
		{"job_list", map[string]any{"filter": "Dep"}, []string{"job", "list", "--filter", "Dep"}},
		{"job_get", map[string]any{"path": "Deploy"}, []string{"job", "get", "Deploy"}},
		{"build_get", map[string]any{"path": "Deploy", "number": 7}, []string{"build", "get", "Deploy", "7"}},
		{"build_log", map[string]any{"path": "Deploy", "number": 7, "human": true}, []string{"--human", "build", "log", "Deploy", "7"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, want, stderr := runRead(t, deps, test.cli...)
			arguments := map[string]any{"operation": test.name}
			for key, value := range test.args {
				arguments[key] = value
			}
			response := callMCP(t, session, "jkins_read", arguments)
			if code != 0 || stderr != "" || response.IsError || mcpText(t, response) != want {
				t.Fatalf("CLI=%d %q %q, MCP=%#v", code, want, stderr, response)
			}
		})
	}
}

func callMCP(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	response, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func mcpText(t *testing.T, response *mcp.CallToolResult) string {
	t.Helper()
	if len(response.Content) != 1 {
		t.Fatalf("expected one text result, got %#v", response.Content)
	}
	content, ok := response.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", response.Content[0])
	}
	return content.Text
}

func TestMCPHelpAndStatusMatchCLI(t *testing.T) {
	deps := cli.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), StateDir: t.TempDir()}
	session := connectMCP(t, deps)
	for _, test := range []struct {
		name string
		args []string
	}{
		{"jkins_help", []string{"help"}},
		{"jkins_status", []string{"auth", "status"}},
	} {
		code, want, stderr := runRead(t, deps, test.args...)
		response := callMCP(t, session, test.name, map[string]any{})
		if code != 0 || stderr != "" || response.IsError || mcpText(t, response) != want {
			t.Errorf("%s: CLI=%d %q %q, MCP=%#v", test.name, code, want, stderr, response)
		}
	}
}

func TestMCPExposesSeparateReadAndGuardedWriteTools(t *testing.T) {
	session := connectMCP(t, cli.Deps{})
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"jkins_read": false, "jkins_help": false, "jkins_status": false, "jkins_build_queue": false}
	for _, tool := range listed.Tools {
		if _, ok := want[tool.Name]; !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		want[tool.Name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing tool %q", name)
		}
	}
}
