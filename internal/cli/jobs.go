package cli

import (
	"fmt"
	"github.com/masonhuemmer/jkins/internal/jenkins"
	"strings"
)

func runJob(args []string, human bool, deps Deps) int {
	if len(args) < 2 {
		fmt.Fprintln(deps.Err, "usage: jkins job list --filter TEXT | --path FOLDER; jkins job get PATH")
		return 2
	}
	switch args[0] {
	case "list":
		if len(args) != 3 || (args[1] != "--filter" && args[1] != "--path") || args[2] == "" {
			fmt.Fprintln(deps.Err, "usage: jkins job list --filter TEXT | --path FOLDER")
			return 2
		}
		client, err := readClient(deps)
		if err != nil {
			return fail(deps.Err, "job list", err)
		}
		var jobs []jenkins.Job
		if args[1] == "--filter" {
			jobs, err = client.ListJobs(args[2])
		} else {
			jobs, err = client.ListFolder(args[2])
		}
		if err != nil {
			return fail(deps.Err, "job list", err)
		}
		var lines []string
		for _, job := range jobs {
			lines = append(lines, fmt.Sprintf("%s  %s  %s", job.Path, job.Kind, job.Color))
		}
		return result(deps.Out, human, jobs, strings.Join(lines, "\n"))
	case "get":
		if len(args) != 2 {
			fmt.Fprintln(deps.Err, "usage: jkins job get PATH")
			return 2
		}
		client, err := readClient(deps)
		if err != nil {
			return fail(deps.Err, "job get", err)
		}
		job, err := client.GetJob(args[1])
		if err != nil {
			return fail(deps.Err, "job get", err)
		}
		readable := fmt.Sprintf("%s  %s  %s", job.Path, job.Status, job.Color)
		if job.LastBuild != nil {
			readable += fmt.Sprintf("\nLast build: #%d %s %s", job.LastBuild.Number, job.LastBuild.Result, job.LastBuild.URL)
		}
		return result(deps.Out, human, job, readable)
	}
	fmt.Fprintln(deps.Err, "invalid job command")
	return 2
}
