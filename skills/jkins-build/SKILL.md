---
name: jkins-build
description: Preview, trigger, and follow Jenkins builds with jkins. Use when a user asks to run a job, supply parameters, or watch a build; deployment targets must be explicit.
---

# Run Jenkins builds with jkins

Find the exact job path with `jkins job list --filter TEXT` or `jkins list-jobs FOLDER`. For a deployment, establish the explicit target from the user's request before triggering it. Check `jkins auth status`; if credentials are absent, ask the user to run `jkins auth login` in a terminal.

For a queued build, preview the target and parameters, then submit only when the user has requested that build:

```sh
jkins build queue Team/Job --param VERSION=1.2
jkins build queue Team/Job --param VERSION=1.2 --execute
```

The first command is a local preview and needs no credential. `--execute` queues one build. Parameters are sent in the POST form body. If submission fails ambiguously, inspect the Jenkins queue or job state before retrying; another submission could queue a duplicate.

Direct `jkins` build syntax is different: `jkins build Team/Job` queues immediately. Read `jkins help build` for the controller's current options. Use `jkins build Team/Job -f -v` when the user wants to follow the run and see output; `-f` lets the build keep running if the local follow is interrupted. Do not substitute the direct command for a preview.

If using MCP, `jkins_build_queue` takes `job_path` and optional `parameters`. It previews by default; `write_opt_in: true` queues the build. MCP cannot follow a build. Read a specific build with `jkins build get PATH NUMBER` or `jkins build log PATH NUMBER` after identifying its number.
