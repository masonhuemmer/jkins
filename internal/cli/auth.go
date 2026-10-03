package cli

import (
	"fmt"
	"strings"

	"github.com/masonhuemmer/jkins/internal/vault"
)

func runAuth(command string, human bool, store vault.Store, deps Deps) int {
	switch command {
	case "login":
		if deps.Terminal == nil || !deps.Terminal.IsTerminal() {
			fmt.Fprintln(deps.Err, "auth login requires a terminal")
			return 1
		}
		user, err := deps.Terminal.ReadLine("Jenkins user: ")
		if err != nil {
			return fail(deps.Err, "read user", err)
		}
		user = strings.TrimSpace(user)
		if user == "" {
			fmt.Fprintln(deps.Err, "user is required")
			return 2
		}
		token, err := deps.Terminal.ReadPassword("Jenkins API token: ")
		if err != nil {
			fmt.Fprintln(deps.Err, "could not read API token")
			return 1
		}
		if strings.TrimSpace(token) == "" {
			fmt.Fprintln(deps.Err, "API token is required")
			return 2
		}
		if err := store.Save(vault.Credential{User: user, Token: token}); err != nil {
			return fail(deps.Err, "save credential", err)
		}
		return result(deps.Out, human, struct {
			Authenticated bool   `json:"authenticated"`
			User          string `json:"user"`
		}{true, user}, "Logged in as "+user)
	case "status":
		credential, present, err := store.Load()
		if err != nil {
			return fail(deps.Err, "read credential", err)
		}
		if !present {
			return result(deps.Out, human, struct {
				Present bool `json:"present"`
			}{false}, "No credential stored")
		}
		return result(deps.Out, human, struct {
			Present bool   `json:"present"`
			User    string `json:"user"`
		}{true, credential.User}, "Credential stored for "+credential.User)
	case "logout":
		if err := store.Delete(); err != nil {
			return fail(deps.Err, "delete credential", err)
		}
		return result(deps.Out, human, struct {
			Authenticated bool `json:"authenticated"`
		}{false}, "Logged out")
	}
	return 2
}
