---
name: jkins-command
description: Use jkins to run Jenkins controller CLI commands beyond its structured job and build operations, including plugin-provided or administrative commands.
---

# Jenkins controller commands through jkins

`jkins` speaks the Jenkins WebSocket CLI protocol directly. It runs commands implemented by the connected controller and its plugins; the available set and options depend on that controller. Discover them before choosing arguments:

```sh
jkins commands
jkins help COMMAND
jkins COMMAND [arguments]
```

For a command name that overlaps a local `jkins` command, use `jkins --jenkins-command COMMAND [arguments]`. For example, `jkins --jenkins-command help` sends `help` to Jenkins. Native commands preserve stdin, stdout, stderr, and Jenkins exit codes. They use the controller's CLI semantics, including immediate execution for `jkins build JOB`.

Use a command that changes Jenkins only when the user's request authorizes that action. For a build or deployment, identify the exact job and deployment target; `jkins build queue JOB` can preview before `--execute`, while direct `jkins build JOB` runs immediately. Before other writes, inspect the controller's `help COMMAND`, identify the exact objects affected, and report the result from that invocation. A failed or interrupted command may already have changed server state; check before retrying.

Run `jkins auth status` to check whether a credential is stored. If absent, ask the user to run `jkins auth login` in a terminal. Use the `README.md` for controller and TLS configuration. The MCP server exposes only structured reads and build queueing, so run other native commands through the local CLI. Controller output may contain secrets; share only what is needed for the task.
