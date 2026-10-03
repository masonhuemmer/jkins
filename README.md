# jkins

`jkins` is a Go CLI for Jenkins. Its structured job and build commands use the HTTP API. Direct commands such as `jkins list-jobs`, `jkins console`, and `jkins build JOB -f -v` use a native Go implementation of Jenkins' WebSocket CLI protocol. They do not launch the Java CLI or use 1Password. The current controller's 72 advertised commands and command-specific verification TODO are in [CLI_PARITY.md](CLI_PARITY.md).

## Build and configure

Go 1.26 or newer is required. Build a local binary with `make build` (equivalent to `go build -o ./jkins ./cmd/jkins`), then run `./jkins help`. Nothing is installed globally. Run `make verify` for the format, vet, and test checks.

Prebuilt releases can be installed with `brew install masonhuemmer/tap/jkins` on macOS, or `scoop bucket add masonhuemmer https://github.com/masonhuemmer/scoop-bucket` followed by `scoop install masonhuemmer/jkins` on Windows. The winget package identifier is `JacobHuemmer.jkins`; its community listing becomes installable after the manifest pull request is accepted. A Chocolatey package is attached to each GitHub release. Download the `.nupkg` from the [releases page](https://github.com/masonhuemmer/jkins/releases) and run `choco install jkins --source PATH_TO_DOWNLOAD_DIRECTORY --version 2.462.3` for the first release. The package will also be available through `choco install jkins` after it is published and approved on Chocolatey Community. Run `jkins --version` for the client release version. `jkins version` asks the controller for its Jenkins version. The first `jkins` release uses `v2.462.3` to identify the Jenkins version used for protocol and command parity review; future client releases can advance independently.

Configure the controller in `jkins/config.json` under Go's platform user config directory before running network commands. For a custom CA bundle, use an absolute path to a PEM certificate file:

```json
{
  "url": "https://jenkins.example.com/",
  "ca_file": "/absolute/path/to/controller-ca.pem"
}
```

Controller URLs must use HTTPS. TLS certificate and hostname verification are enabled by default; an invalid or unreadable CA bundle is rejected. For a controller with a private or expired certificate, any network command can use `--skip-tls-verify`. This disables certificate and hostname checks for that invocation, matching `curl -k`; it takes precedence over `ca_file`. Use it only on a trusted network because an impersonating server could receive the API token. Remove the option after certificate renewal.

`./jkins auth login` requires a real terminal. It prompts for the Jenkins user and reads the API token without echoing it. There is no token argument, environment-variable credential fallback, or MCP login tool. `auth status` reports only whether a credential is present and its user; `auth logout` removes it. The token is stored in an age-encrypted vault at `jkins/vault.json` under the platform state directory, with an independent X25519 identity at `jkins/keys/identity.txt`. Files are private (`0600`) and owned directories are `0700`. The identity key and ciphertext share the same host, so this protects against casual vault-file disclosure, not a full host compromise.

## Commands

Data and results are JSON by default. Add `--human` for readable text; help is always text.

```sh
./jkins auth status
./jkins job list --filter Deploy
./jkins --skip-tls-verify job list --filter Deploy
./jkins job list --path Folder
./jkins job get Folder/Job
./jkins build get Folder/Job 42
./jkins build log Folder/Job 42
./jkins build queue Deploy/PROD --param VERSION=1.2
./jkins build queue Deploy/PROD --param VERSION=1.2 --execute
```

Job and build reads require an explicit path or filter. `build queue` shows the exact target and parameters without submitting by default. `--execute` submits one POST. For a deployment job, choose the explicit deployment target and inspect the preview first. Parameters go in the POST form body, not the URL. The command does not follow redirects or issue a second request.

Native Jenkins CLI commands are available directly in the same Go binary:

```sh
./jkins --skip-tls-verify commands
./jkins --skip-tls-verify help build
./jkins list-jobs 'Team'
./jkins console 'Team/Job' lastBuild -n 200
./jkins build 'Team/Job' -f -v
```

`commands` asks the controller for its current command list. `help COMMAND` asks for current syntax. Direct commands preserve stdin, stdout, stderr, and Jenkins exit codes. They run with Jenkins CLI semantics: **`jkins build JOB` queues immediately**, while `jkins build queue JOB` remains a preview until `--execute`. Local `jkins help` shows this CLI's help; `jkins --jenkins-command help` forces the controller's `help` command. The same flag resolves any command-name collision. The direct command path emits the controller's raw output, which may contain secrets from Jenkins or plugins.

The CLI redacts the stored token and several known encodings of it from displayed controller text and does not display raw HTTP error bodies. A console log can contain other secrets that `jkins` cannot recognize; log display is not a general secret-safety guarantee.

## MCP

Start the stdio server with `./jkins mcp serve`. Configure an MCP client to launch the local absolute path to the binary with arguments `["mcp", "serve"]`; keep protocol stdout attached to the client. The server exposes:

| Tool | Inputs and behavior |
| --- | --- |
| `jkins_help` | No inputs; CLI help text. |
| `jkins_status` | No inputs; stored credential presence and user only. |
| `jkins_read` | `operation` is `job_list`, `job_get`, `build_get`, or `build_log`; use `filter` or `path` for a list, `path` for a job, and `path` plus positive `number` for a build. Optional `human` requests text. |
| `jkins_build_queue` | `job_path` and optional `parameters` map; previews by default. Set `write_opt_in: true` to submit one build. Optional `human` requests text. |

MCP uses the same credential vault and controller configuration as the CLI. Set up credentials with terminal-only `auth login` before authenticated reads or a queue submission. A queue preview needs no credential. Tool results use the CLI's safe JSON or text results; protocol frames use stdout and diagnostics use stderr. MCP exposes only the structured subset above, not arbitrary Jenkins CLI commands.

Automated tests use only a fake local controller. Read-only probes verified the structured job list and native `help`, `version`, `who-am-i`, and `list-jobs` commands. No live build was submitted.

## Agent skills

The repository includes focused, self-contained skills under `skills/`:

| Skill | Use |
| --- | --- |
| [`jkins-setup`](skills/jkins-setup/SKILL.md) | Configure the controller, TLS, and credential vault. |
| [`jkins-read`](skills/jkins-read/SKILL.md) | Discover jobs and inspect builds or logs. |
| [`jkins-build`](skills/jkins-build/SKILL.md) | Preview, trigger, and follow builds. |
| [`jkins-command`](skills/jkins-command/SKILL.md) | Discover and run other controller or plugin CLI commands. |

Copy each skill directory into an agent's skill root, or symlink it from a dotfiles repository. For example, Codex can discover them under `.agents/skills/<skill-name>/SKILL.md` and Claude under `.claude/skills/<skill-name>/SKILL.md`. These skills require the local `jkins` binary and controller configuration described above. No `jkins skill` command is needed.
