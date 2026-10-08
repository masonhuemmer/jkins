---
name: jkins-setup
description: Configure the jkins controller URL, TLS trust, and local API-token vault. Use for first-time setup, login, or certificate troubleshooting.
---

# Set up jkins

Use `jkins --version` to check the installed client and `jkins help` for local commands. Configure `jkins/config.json` under the platform user config directory with an HTTPS controller root URL:

```json
{"url":"https://jenkins.example.com/"}
```

For a private certificate authority, add `"ca_file":"/absolute/path/to/ca.pem"` to that JSON object. The CA file must be an absolute path to a PEM certificate. `--skip-tls-verify` bypasses certificate and hostname checks for one invocation; prefer a trusted CA because the override exposes the API token to an impersonating server. It does not bypass a network or device-access policy.

Run `jkins auth status` to see whether a local credential is stored. A user can run `jkins auth login` in a real terminal to enter a Jenkins username and API token without echoing the token. `auth status` reports only presence and username, not whether the controller accepts the token. `jkins auth logout` removes the stored credential. Keep tokens out of command arguments, config files, and shared output.

Once configured and authenticated, `jkins who-am-i` tests the controller identity through its WebSocket command path. If the controller's current command list is needed, use `jkins commands`. For platform-specific install and state paths, see the repository's `README.md`.
