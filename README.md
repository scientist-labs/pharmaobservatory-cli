# pharmaobservatory-cli

`po`, the Pharma Observatory command line client. So far: `po version`, `po login`,
`po logout` and `po whoami`. The full README comes later.

## Build from source

Requires Go 1.24 or newer.

```sh
go build -o po ./cmd/po
./po version
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for tests, lint and the release dry run.

## Sign in

```sh
po login                  # opens your browser; loopback redirect on 127.0.0.1:53682
po login --no-browser     # prints the URL; paste the callback URL (or just its code) back
po whoami                 # profile, origin, sub and roles; --json prints the raw body
po logout                 # removes the stored credential for the profile
```

Profiles: `prod` (default) and `staging` are built in; pick one with `--profile`, `$PO_PROFILE` or
`default_profile` in `config.yml`. `po login --profile NAME --origin URL [--client-id ID]` creates a
custom profile.

The refresh token lives in the OS keychain (macOS Keychain, Linux Secret Service, Windows
Credential Manager). Where none is available, or with `PO_CREDENTIAL_STORE=file`, it lives in
`credentials.json` (mode 0600) in the config directory (`$PO_CONFIG_DIR`, else
`$XDG_CONFIG_HOME/po`, else `~/.config/po`). Logout deletes it locally and does not revoke it
server-side.
