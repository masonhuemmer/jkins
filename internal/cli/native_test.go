package cli_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/masonhuemmer/jkins/internal/cli"
)

func TestNativeCLIUsesVaultAndWebSocketProtocol(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	commands := make(chan []string, 4)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cli/ws" {
			t.Errorf("endpoint: %s", r.URL.Path)
		}
		if user, token, ok := r.BasicAuth(); !ok || user != "alice" || token != "unique-secret-token" {
			t.Error("WebSocket handshake did not use vault credential")
		}
		if r.Header.Get("Origin") != "https://"+r.Host {
			t.Errorf("origin: %q", r.Header.Get("Origin"))
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var args []string
		var input strings.Builder
		started, ended := false, false
		for !ended {
			kind, frame, err := conn.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || len(frame) == 0 {
				t.Errorf("invalid client frame: kind=%d err=%v", kind, err)
				return
			}
			switch frame[0] {
			case 0:
				if len(frame) < 3 || int(binary.BigEndian.Uint16(frame[1:3])) != len(frame)-3 {
					t.Error("invalid ARG frame")
					return
				}
				args = append(args, string(frame[3:]))
			case 2:
				if string(frame[3:]) != "UTF-8" {
					t.Errorf("encoding frame: %q", frame)
				}
			case 1:
				if string(frame[3:]) != "en_US" {
					t.Errorf("locale frame: %q", frame)
				}
			case 3:
				started = true
			case 5:
				input.Write(frame[1:])
			case 6:
				ended = true
			default:
				t.Errorf("client operation: %d", frame[0])
			}
		}
		if !started || input.String() != "source from stdin\n" {
			t.Errorf("start=%v stdin=%q", started, input.String())
		}
		commands <- args
		conn.WriteMessage(websocket.BinaryMessage, append([]byte{7}, []byte("from stdout\n")...))
		conn.WriteMessage(websocket.BinaryMessage, append([]byte{8}, []byte("from stderr\n")...))
		conn.WriteMessage(websocket.BinaryMessage, []byte{4, 0, 0, 0, 7})
	}))
	defer server.Close()
	deps := readDepsWithToken(t, "", "unique-secret-token")
	deps.In = strings.NewReader("source from stdin\n")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"url": server.URL, "ca_file": caFile})
	if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input []string
		want  string
	}{
		{[]string{"commands"}, "help"},
		{[]string{"--jenkins-command", "help"}, "help"},
		{[]string{"help", "build"}, "help,build"},
		{[]string{"list-jobs", "Team"}, "list-jobs,Team"},
		{[]string{"build", "Job", "-f", "-v"}, "build,Job,-f,-v"},
		{[]string{"--jenkins-command", "build", "get"}, "build,get"},
	} {
		deps.In = strings.NewReader("source from stdin\n")
		code, out, stderr := runRead(t, deps, test.input...)
		if code != 7 || out != "from stdout\n" || stderr != "from stderr\n" {
			t.Fatalf("%v: code=%d out=%q err=%q", test.input, code, out, stderr)
		}
		select {
		case args := <-commands:
			if strings.Join(args, ",") != test.want {
				t.Fatalf("%v: sent %v, want %s", test.input, args, test.want)
			}
		default:
			t.Fatalf("%v: no command reached the fake controller", test.input)
		}
	}
}

func TestNativeCLIStreamsLargeInputAndBinaryOutput(t *testing.T) {
	input := bytes.Repeat([]byte("input\x00"), 20_000)
	output := []byte{0, 1, 2, 255, '\n'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var received []byte
		chunks := 0
		for {
			kind, frame, err := conn.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage || len(frame) == 0 {
				t.Errorf("client frame: kind=%d err=%v", kind, err)
				return
			}
			switch frame[0] {
			case 5:
				if len(frame)-1 > 60_000 {
					t.Errorf("stdin chunk too large: %d", len(frame)-1)
				}
				chunks++
				received = append(received, frame[1:]...)
			case 6:
				if chunks < 2 || !bytes.Equal(received, input) {
					t.Errorf("stdin chunks=%d bytes=%d", chunks, len(received))
				}
				conn.WriteMessage(websocket.BinaryMessage, append([]byte{7}, output...))
				conn.WriteMessage(websocket.BinaryMessage, []byte{4, 0, 0, 0, 0})
				return
			}
		}
	}))
	defer server.Close()
	deps := readDepsWithToken(t, server.URL, "unique-secret-token")
	deps.In = bytes.NewReader(input)
	var out, stderr bytes.Buffer
	deps.Out, deps.Err = &out, &stderr
	if code := cli.Run([]string{"--jenkins-command", "some-plugin-command"}, deps); code != 0 || !bytes.Equal(out.Bytes(), output) || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%x stderr=%q", code, out.Bytes(), stderr.String())
	}
}

func TestClientVersionDoesNotContactJenkins(t *testing.T) {
	code, out, stderr := runRead(t, cli.Deps{}, "--version")
	if code != 0 || out != "jkins dev\n" || stderr != "" {
		t.Fatalf("version: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestNativeCLIReportsConnectionLossDuringStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if len(frame) > 0 && frame[0] == 5 {
				return // Disconnect before END_STDIN and EXIT.
			}
		}
	}))
	defer server.Close()
	deps := readDepsWithToken(t, server.URL, "unique-secret-token")
	deps.In = bytes.NewReader(bytes.Repeat([]byte("x"), 120_000))
	result := make(chan struct {
		code int
		out  string
		err  string
	}, 1)
	go func() {
		code, out, err := runRead(t, deps, "--jenkins-command", "some-plugin-command")
		result <- struct {
			code int
			out  string
			err  string
		}{code, out, err}
	}()
	select {
	case got := <-result:
		if got.code == 0 || got.out != "" || !strings.Contains(got.err, "CLI ") {
			t.Fatalf("disconnect: code=%d stdout=%q stderr=%q", got.code, got.out, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not return after WebSocket disconnect")
	}
}

func TestNativeCLIPreservesFollowOutputWhenConnectionDrops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if len(frame) > 0 && frame[0] == 6 {
				conn.WriteMessage(websocket.BinaryMessage, []byte{7, 'b', 'u', 'i', 'l', 'd', '\n'})
				return // A followed build lost its connection before EXIT.
			}
		}
	}))
	defer server.Close()
	deps := readDepsWithToken(t, server.URL, "unique-secret-token")
	code, out, stderr := runRead(t, deps, "build", "fixture-job", "-f")
	if code == 0 || out != "build\n" || !strings.Contains(stderr, "CLI connection closed before exit status") {
		t.Fatalf("follow disconnect: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestNativeCLIHonorsExplicitTLSOverride(t *testing.T) {
	server := expiredTLSServer(t, "127.0.0.1", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			_, frame, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if len(frame) > 0 && frame[0] == 6 {
				conn.WriteMessage(websocket.BinaryMessage, []byte{4, 0, 0, 0, 0})
				return
			}
		}
	})
	deps := readDepsWithToken(t, "", "unique-secret-token")
	config, _ := json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runRead(t, deps, "who-am-i"); code == 0 || !strings.Contains(stderr, "CLI connection failed") {
		t.Fatalf("expired certificate accepted by default: %d %q", code, stderr)
	}
	if code, out, stderr := runRead(t, deps, "--skip-tls-verify", "who-am-i"); code != 0 || out != "" || stderr != "" {
		t.Fatalf("native TLS override failed: %d %q %q", code, out, stderr)
	}
}

func TestNativeCLIRejectsHandshakeRedirect(t *testing.T) {
	redirects := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects++
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("vault credential reached redirect target")
		}
	}))
	defer target.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	deps := readDepsWithToken(t, "", "unique-secret-token")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]string{"url": server.URL, "ca_file": caFile})
	if err := os.WriteFile(deps.ConfigPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runRead(t, deps, "version")
	if code == 0 || out != "" || !strings.Contains(stderr, "HTTP 302") || redirects != 0 {
		t.Fatalf("redirect: code=%d out=%q err=%q target requests=%d", code, out, stderr, redirects)
	}
}
