# pharmaobservatory-cli

`po`, the Pharma Observatory command line client. So far: `po version`, `po login`,
`po logout`, `po whoami`, the page commands and `po api`. The full README comes later.

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

## Page commands

One command group per page (`biomarkers`, `cell-lines`, `orgs`), generated from the API document
inside the pinned SDK release. Output is the server's JSON body, unchanged; tables and CSV come later.

```sh
po biomarkers list --kind gene --limit 5 --json   # GET /api/v1/biomarkers; query params are flags
po biomarkers get EGFR --json                      # GET /api/v1/biomarkers/EGFR
po biomarkers get EGFR evidence_over_time --json   # GET /api/v1/biomarkers/EGFR/evidence_over_time
po biomarkers hub_tier_tables --json               # collection section: GET /api/v1/biomarkers/hub_tier_tables
po cell-lines status_counts --species human
po orgs get company:123 identity                   # ids are passed through verbatim
```

Put the id and section before any flags. `po <page> --help` lists the sections; a parameter named
like a global flag (for example `--json`) is exposed as `--param-json`. A section whose key is also
a command name (`list`, `get`, `help`, `completion`, `section`) is reached as
`po <page> section <key>`, and a page whose slug is a top-level command as `po page <slug>`.

`po api <path>` is the escape hatch: an authenticated GET that prints the body. A relative path is
under `/api/v1/`; a path starting with `/` is used as given; `-F key=value` adds query parameters.

```sh
po api biomarkers -F kind=gene -F limit=5
po api /api/v1/whoami
```
