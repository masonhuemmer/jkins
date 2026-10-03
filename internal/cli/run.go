package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/masonhuemmer/jkins/internal/config"
	"github.com/masonhuemmer/jkins/internal/jenkins"
	"github.com/masonhuemmer/jkins/internal/vault"
)

type Terminal interface {
	IsTerminal() bool
	ReadLine(prompt string) (string, error)
	ReadPassword(prompt string) (string, error)
}

type Deps struct {
	ConfigPath    string
	StateDir      string
	In            io.Reader
	Out           io.Writer
	Err           io.Writer
	Terminal      Terminal
	ControllerURL string // Injected local test controller. Operator config remains HTTPS-only.
	skipTLSVerify bool
}

// Version is set from the release tag when building distribution archives.
var Version = "dev"

const help = `jkins: Jenkins CLI

Usage:
  jkins help
  jkins --version
  jkins auth login
  jkins auth status
  jkins auth logout
  jkins job list --filter TEXT
  jkins job list --path FOLDER
  jkins job get PATH
  jkins build get PATH NUMBER
  jkins build log PATH NUMBER
  jkins build queue JOB [--param KEY=VALUE]... [--execute]
  jkins commands
  jkins help JENKINS_COMMAND
  jkins list-jobs FOLDER
  jkins build JOB [-f] [-v] [-p KEY=VALUE]
  jkins console JOB [BUILD] [-f] [-n N]
  jkins who-am-i
  jkins mcp serve

Global option: --human displays readable text instead of JSON.
Use --jenkins-command to force a server CLI command when a local command name overlaps.
TLS option: --skip-tls-verify skips certificate and hostname checks for this invocation.
Use only on a trusted network; the API token can be exposed to an impersonating server.
Login requires a terminal and reads an API token without echoing it.
Build queue previews the explicit target and parameters by default; --execute submits one build.
Direct Jenkins commands use the native Go WebSocket client and follow Jenkins CLI semantics.
For example, jkins build JOB queues immediately. Run jkins commands for the live command list.
For a deployment job, specify the exact deployment target and review the preview before --execute.
MCP exposes read, help, and status tools. Its build queue tool previews unless write_opt_in is true.
`

func Run(args []string, deps Deps) int {
	if deps.Out == nil {
		deps.Out = io.Discard
	}
	if deps.Err == nil {
		deps.Err = io.Discard
	}
	human := false
	forceJenkinsCommand := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--human" {
			human = true
		} else if arg == "--skip-tls-verify" {
			deps.skipTLSVerify = true
		} else if arg == "--jenkins-command" {
			forceJenkinsCommand = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	if len(filtered) == 0 {
		if forceJenkinsCommand {
			fmt.Fprintln(deps.Err, "Jenkins command is required")
			return 2
		}
		fmt.Fprint(deps.Out, help)
		return 0
	}
	if len(filtered) == 1 && filtered[0] == "--version" {
		fmt.Fprintf(deps.Out, "jkins %s\n", Version)
		return 0
	}
	if forceJenkinsCommand {
		return runNativeCLI(filtered, deps)
	}
	if len(filtered) == 1 && (filtered[0] == "help" || filtered[0] == "--help" || filtered[0] == "-h") {
		fmt.Fprint(deps.Out, help)
		return 0
	}
	if len(filtered) == 2 && filtered[0] == "mcp" && filtered[1] == "serve" {
		return runMCP(deps)
	}
	if filtered[0] == "mcp" {
		fmt.Fprintln(deps.Err, "invalid command; run jkins help")
		return 2
	}
	if len(filtered) >= 2 && filtered[0] == "build" {
		if filtered[1] == "get" || filtered[1] == "log" || filtered[1] == "queue" {
			return runBuild(filtered[1:], human, deps)
		}
		return runNativeCLI(filtered, deps)
	}
	if len(filtered) >= 2 && filtered[0] == "job" {
		return runJob(filtered[1:], human, deps)
	}
	if len(filtered) == 1 && filtered[0] == "commands" {
		return runNativeCLI([]string{"help"}, deps)
	}
	if filtered[0] != "auth" || len(filtered) != 2 || (filtered[1] != "login" && filtered[1] != "status" && filtered[1] != "logout") {
		return runNativeCLI(filtered, deps)
	}
	paths, err := config.DefaultPaths()
	if err != nil {
		return fail(deps.Err, "resolve paths", err)
	}
	if deps.ConfigPath != "" {
		paths.ConfigFile = deps.ConfigPath
	}
	if deps.StateDir != "" {
		paths.StateDir = deps.StateDir
	}
	if _, err := config.Load(paths.ConfigFile); err != nil {
		return fail(deps.Err, "load config", err)
	}
	return runAuth(filtered[1], human, vault.Store{Dir: paths.StateDir}, deps)
}

func runNativeCLI(args []string, deps Deps) int {
	client, err := readClient(deps)
	if err != nil {
		return fail(deps.Err, "Jenkins CLI", err)
	}
	code, err := client.RunCommand(args, deps.In, deps.Out, deps.Err)
	if err != nil {
		return fail(deps.Err, "Jenkins CLI", err)
	}
	return code
}

func readClient(deps Deps) (*jenkins.Client, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	if deps.ConfigPath != "" {
		paths.ConfigFile = deps.ConfigPath
	}
	if deps.StateDir != "" {
		paths.StateDir = deps.StateDir
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return nil, err
	}
	if deps.ControllerURL != "" {
		cfg.URL = deps.ControllerURL
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("controller URL is not configured; set url in %s", paths.ConfigFile)
	}
	credential, present, err := (vault.Store{Dir: paths.StateDir}).Load()
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("no credential stored")
	}
	return jenkins.New(cfg, credential, deps.ControllerURL != "", deps.skipTLSVerify)
}

func result(out io.Writer, human bool, value any, readable string) int {
	if human {
		fmt.Fprintln(out, readable)
		return 0
	}
	if err := json.NewEncoder(out).Encode(value); err != nil {
		return 1
	}
	return 0
}

func fail(out io.Writer, action string, err error) int {
	fmt.Fprintf(out, "%s: %s\n", action, strings.TrimSpace(err.Error()))
	return 1
}
