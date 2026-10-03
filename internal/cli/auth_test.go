package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/masonhuemmer/jkins/internal/cli"
)

type fakeTerminal struct {
	tty         bool
	user, token string
	reads       int
}

func (f *fakeTerminal) IsTerminal() bool                    { return f.tty }
func (f *fakeTerminal) ReadLine(string) (string, error)     { f.reads++; return f.user, nil }
func (f *fakeTerminal) ReadPassword(string) (string, error) { f.reads++; return f.token, nil }

func run(t *testing.T, state string, terminal *fakeTerminal, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	status := cli.Run(args, cli.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), StateDir: state, In: strings.NewReader("should-not-read"), Out: &out, Err: &errOut, Terminal: terminal})
	return status, out.String(), errOut.String()
}

func TestHelpAndAuthenticationLifecycle(t *testing.T) {
	state := filepath.Join(t.TempDir(), "jkins")
	term := &fakeTerminal{tty: true, user: "alice", token: "unique-secret-token"}
	if code, out, _ := run(t, state, term, "help"); code != 0 || !strings.Contains(out, "auth login") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, out, stderr := run(t, state, term, "auth", "status"); code != 0 || !strings.Contains(out, `"present":false`) || stderr != "" {
		t.Fatalf("empty status: %d %q %q", code, out, stderr)
	}
	if code, out, stderr := run(t, state, term, "auth", "login"); code != 0 || stderr != "" || strings.Contains(out, term.token) {
		t.Fatalf("login: %d %q %q", code, out, stderr)
	}
	code, out, stderr := run(t, state, term, "auth", "status")
	var status struct {
		Present bool   `json:"present"`
		User    string `json:"user"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &status) != nil || !status.Present || status.User != "alice" || strings.Contains(out+stderr, term.token) {
		t.Fatalf("status: %d %q %q", code, out, stderr)
	}
	if code, out, stderr := run(t, state, term, "--human", "auth", "status"); code != 0 || !strings.Contains(out, "alice") || strings.Contains(out+stderr, term.token) {
		t.Fatalf("human status: %d %q %q", code, out, stderr)
	}
	if code, out, stderr := run(t, state, term, "auth", "logout"); code != 0 || strings.Contains(out+stderr, term.token) {
		t.Fatalf("logout: %d %q %q", code, out, stderr)
	}
	if code, out, _ := run(t, state, term, "auth", "status"); code != 0 || !strings.Contains(out, `"present":false`) {
		t.Fatalf("status after logout: %d %q", code, out)
	}
}

func TestLoginRequiresTerminalAndNeverAcceptsTokenFlag(t *testing.T) {
	state := filepath.Join(t.TempDir(), "jkins")
	term := &fakeTerminal{tty: false, user: "alice", token: "unique-secret-token"}
	if code, _, stderr := run(t, state, term, "auth", "login"); code == 0 || term.reads != 0 || strings.Contains(stderr, term.token) {
		t.Fatalf("non-TTY login: %d reads=%d %q", code, term.reads, stderr)
	}
	term.tty = true
	if code, _, stderr := run(t, state, term, "auth", "login", "--token=unique-secret-token"); code == 0 || term.reads != 0 || strings.Contains(stderr, term.token) {
		t.Fatalf("token flag: %d reads=%d %q", code, term.reads, stderr)
	}
	term.token = ""
	if code, _, _ := run(t, state, term, "auth", "login"); code == 0 {
		t.Fatal("empty token accepted")
	}
}

func TestEnvironmentDoesNotSupplyCredentialsAndCorruptionFailsClosed(t *testing.T) {
	state := filepath.Join(t.TempDir(), "jkins")
	t.Setenv("JENKINS_USER_ID", "environment-user")
	t.Setenv("JENKINS_API_TOKEN", "environment-token")
	term := &fakeTerminal{tty: true, user: "alice", token: "unique-secret-token"}
	if code, out, _ := run(t, state, term, "auth", "status"); code != 0 || strings.Contains(out, "environment-user") || !strings.Contains(out, `"present":false`) {
		t.Fatalf("env status: %d %q", code, out)
	}
	if code, _, _ := run(t, state, term, "auth", "login"); code != 0 {
		t.Fatal("login failed")
	}
	if err := os.WriteFile(filepath.Join(state, "vault.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"auth", "status"}, {"auth", "login"}, {"auth", "logout"}} {
		if code, out, stderr := run(t, state, term, args...); code == 0 || strings.Contains(out+stderr, term.token) || strings.Contains(out+stderr, "environment-token") {
			t.Fatalf("corrupt %v: %d %q %q", args, code, out, stderr)
		}
	}
}
