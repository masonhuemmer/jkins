package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/masonhuemmer/jkins/internal/jenkins"
)

func runBuild(args []string, human bool, deps Deps) int {
	if len(args) > 0 && args[0] == "queue" {
		return runQueue(args[1:], human, deps)
	}
	if len(args) != 3 || (args[0] != "get" && args[0] != "log") {
		fmt.Fprintln(deps.Err, "usage: jkins build get|log PATH NUMBER")
		return 2
	}
	number, err := strconv.Atoi(args[2])
	if err != nil || number < 1 {
		fmt.Fprintln(deps.Err, "build number must be a positive integer")
		return 2
	}
	client, err := readClient(deps)
	if err != nil {
		return fail(deps.Err, "build "+args[0], err)
	}
	if args[0] == "get" {
		build, err := client.GetBuild(args[1], number)
		if err != nil {
			return fail(deps.Err, "build get", err)
		}
		return result(deps.Out, human, build, fmt.Sprintf("#%d  %s  %s  started %d  duration %d ms", build.Number, build.Result, build.URL, build.Timestamp, build.Duration))
	}
	log, err := client.BuildLog(args[1], number)
	if err != nil {
		return fail(deps.Err, "build log", err)
	}
	if human {
		fmt.Fprint(deps.Out, log)
		return 0
	}
	return result(deps.Out, false, struct {
		Log string `json:"log"`
	}{log}, "")
}

type queueResult struct {
	JobPath    string            `json:"job_path"`
	Parameters map[string]string `json:"parameters"`
	Executed   bool              `json:"executed"`
	Queued     bool              `json:"queued"`
	Location   string            `json:"location,omitempty"`
}

func runQueue(args []string, human bool, deps Deps) int {
	if len(args) == 0 {
		fmt.Fprintln(deps.Err, "usage: jkins build queue JOB [--param KEY=VALUE]... [--execute]")
		return 2
	}
	job := args[0]
	if strings.HasPrefix(job, "--") {
		fmt.Fprintln(deps.Err, "job path is required before options")
		return 2
	}
	if _, err := jenkins.JobPath(job); err != nil {
		return fail(deps.Err, "build queue", err)
	}
	params := make(map[string]string)
	execute := false
	for i := 1; i < len(args); i++ {
		if args[i] == "--execute" {
			if execute {
				fmt.Fprintln(deps.Err, "--execute may be used once")
				return 2
			}
			execute = true
			continue
		}
		if args[i] != "--param" || i+1 == len(args) {
			fmt.Fprintln(deps.Err, "usage: jkins build queue JOB [--param KEY=VALUE]... [--execute]")
			return 2
		}
		i++
		key, value, ok := strings.Cut(args[i], "=")
		if !ok || !validParameterKey(key) {
			fmt.Fprintln(deps.Err, "invalid parameter key")
			return 2
		}
		params[key] = value
	}
	preview := queueResult{JobPath: job, Parameters: params}
	if execute {
		client, err := readClient(deps)
		if err != nil {
			return fail(deps.Err, "build queue", err)
		}
		location, err := client.QueueBuild(job, params)
		if err != nil {
			return fail(deps.Err, "build queue", err)
		}
		preview.Executed, preview.Queued, preview.Location = true, true, location
		return result(deps.Out, human, preview, fmt.Sprintf("Queued: %s  location: %s", job, location))
	}
	return result(deps.Out, human, preview, fmt.Sprintf("Preview: %s  parameters: %v", job, params))
}

func validParameterKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && (r == '-' || r == '.' || r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
