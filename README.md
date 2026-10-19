# pharmaobservatory-cli

A command-line client for the [Pharma Observatory](https://pharmaobservatory.com) REST API. It talks
to the API only, and needs a plan with API access.

The command is `po` in every example below. The name may change before v0.1.0.

## Install

> **Not yet released. Packages appear here with v0.1.0.** None of the install paths below works today.
> Until then, [build from source](#build-from-source).

Homebrew (macOS and Linux):

```sh
brew install scientist-labs/pharmaobservatory/pharmaobservatory
```

Debian and Ubuntu (apt), served at `packages.pharmaobservatory.com`:

```sh
sudo install -d /usr/share/keyrings
curl -fsSL https://packages.pharmaobservatory.com/pharmaobservatory.gpg \
  | sudo tee /usr/share/keyrings/pharmaobservatory-archive-keyring.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/pharmaobservatory-archive-keyring.gpg] https://packages.pharmaobservatory.com/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/pharmaobservatory.list
sudo apt-get update && sudo apt-get install pharmaobservatory
```

Fedora, RHEL and derivatives (dnf or yum):

```sh
sudo curl -fsSL -o /etc/yum.repos.d/pharmaobservatory.repo https://packages.pharmaobservatory.com/rpm/pharmaobservatory.repo
sudo dnf install pharmaobservatory
```

The repository metadata is signed; the individual packages are not.

Release archives (Linux, macOS, Windows) will be attached to each GitHub release. Verify one with
[cosign](https://github.com/sigstore/cosign) (keyless; replace the tag):

```sh
cosign verify-blob \
  --certificate-identity 'https://github.com/scientist-labs/pharmaobservatory-cli/.github/workflows/release.yml@refs/tags/v0.1.0' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
sha256sum --check --ignore-missing checksums.txt
```

## Sign in

```sh
po login                  # opens your browser; loopback redirect on 127.0.0.1:53682
po login --no-browser     # prints the URL; sign in anywhere, then paste the callback URL (or just its code)
po whoami                 # profile, origin, who, organization, plan, roles and remaining quota; --json prints the raw body
po logout                 # removes the stored credential for the profile
```

Profiles: `prod` (default) and `staging` are built in. Pick one with `--profile`, `$PO_PROFILE` or
`default_profile` in `config.yml`. Create a custom profile with
`po login --profile NAME --origin URL [--client-id ID]`.

The refresh token lives in the OS keychain (macOS Keychain, Linux Secret Service, Windows Credential
Manager). Where none is available, or with `PO_CREDENTIAL_STORE=file`, it lives in `credentials.json`
(mode 0600) in the config directory (`$PO_CONFIG_DIR`, else `$XDG_CONFIG_HOME/po`, else `~/.config/po`).
`po logout` deletes it locally and does not revoke it server-side.

## Commands

One command group per object type (`biomarkers`, `cell-lines`, `companies`, `conditions`, `legal-entities`,
`sites`, `trials`),
plus `datasets` for the catalog, generated from the API document. The shapes:

```sh
po companies list                                  # the object type's primary dataset
po companies get <id>                              # one record and the datasets reachable from it
po companies get <id> sponsor-trials               # one dataset of a record
po datasets list                                   # every published dataset
po datasets get <name>                             # one dataset's grain and columns
```

Real examples: `po companies get <id> sponsor-trials --scope group`, `po sites get <id> site-trials`,
`po biomarkers get ESR1 biomarker-evidence`, `po trials get NCT01234567 trial-drugs`, `po conditions get
D001943 condition-trials`. Command and dataset names are the API's names with `-` for
`_`, and the API's own spelling works too (`po cell_lines list`, `po sites get <id> site_trials`). Ids
are passed through verbatim. Put the id and dataset before any flags. `po <object> --help` lists the
datasets; `po <object> get <id> <dataset> --help` lists a dataset's flags. A parameter named like a
global flag (for example `--json`) is exposed as `--param-json`.

Every dataset read takes the paging and shaping flags the API document declares (`--limit`, `--cursor`,
`--sort`, `--group-by`, `--measure`, and `--scope` on company datasets), plus `--filter`, repeatable,
which uses the server's filter grammar keyed by column:

```sh
po biomarkers list --filter gene_symbol=EGFR --filter evidence_tier.lte=2 --sort -n_trial_mention
po biomarkers list --group-by biomarker_kind --measure count
```

`--filter column=value` is equality (a repeated column is an IN list); `column.gt`, `.gte`, `.lt`,
`.lte`, `.ne`, `.contains` and `.null` are the other operators. The server checks every filter.

### Output

The default is a table, with a footer on stderr (source credit, as-of date, paging). Other forms:

```sh
po biomarkers list --limit 5 --json   # the server's body, unchanged
po biomarkers list --limit 5 --csv    # CSV built from the response columns
```

A record prints its fields (attribute datasets folded in as `dataset.field`), then the datasets
reachable from it. The footer names the withheld columns when the server withheld any, and says when
the server answered for a retired id with its current one (`redirected: <old> is now <new>`); both
print even with `--quiet`.

`--json` and `--csv` cannot be combined. `--quiet` (alias `--no-footer`) drops the as-of and paging
lines.

CSV cells are written exactly as the server returned them. A value that starts with `=`, `+`, `-` or
`@` may be evaluated as a formula by a spreadsheet, so open CSV files from untrusted queries with care.

### Paging with `--all`

`--all` follows the cursor to the last page and prints one table (or one CSV). It cannot be combined
with `--json`; use `--cursor` to page by hand. The walk stops early, prints what it has, marks the
output partial and exits 9 when:

- the operation takes no `--cursor` (a record or the catalog);
- the server returns a cursor it already returned;
- it reaches 1000 pages.

A failure partway through (for example a 429) also stops the walk. `--filter` applies to every page.

### SQL with `query`

`po query` runs one SQL `SELECT` over one published dataset through `POST /api/v1/query` and prints
the first page as a table. SQL requires an authenticated token: sign in with `po login` first. Pass
`-` to read the SQL from stdin.

```sh
# an aggregate
po query "SELECT biomarker_kind, count(*) AS n FROM biomarkers GROUP BY biomarker_kind"
# a filter
po query "SELECT display_name, gene_symbol FROM biomarkers WHERE gene_symbol = 'EGFR'" --csv
```

The server stores the full result for 24 hours and enforces every limit: rows returned per page, rows
stored, statement length and timeout. When the result was cut short, the server's notice prints on
stderr. The footer names the stored result and the next page: `po query --result <result_id> --cursor
<cursor>` fetches it through `GET /api/v1/results/{result_id}`, and `--all` follows the cursor to the
end of the stored result. A walk that meets `RESULT_EXPIRED` or `GENERATION_CHANGED` prints what it
fetched, quotes the server's message and exits 9.

The CLI does not check the SQL. A refused query prints the server's `error_code`, `message`, `hint`
and `docs` on one line and exits by the table below (most refusals exit 2). `--json` prints the
server's body unchanged, including its `attribution` and `source_credit`.

### The `api` escape hatch

`po api <path>` makes an authenticated GET and prints the body. A relative path is under `/api/v1/`; a
path starting with `/` is used as given. `-F key=value` adds a query parameter.

```sh
po api biomarkers -F kind=gene -F limit=5
po api /api/v1/whoami
```

## Source credit

Restricted data carries a source credit. It prints on stderr, and as trailing `# Source:` lines in CSV
output and in piped tables. It cannot be suppressed, including with `--quiet`. To skip those lines
when reading CSV in pandas, use `comment='#'`:

```python
import pandas as pd
df = pd.read_csv("out.csv", comment="#")
```

## Rate limits and errors

A 429 exits 6 and says which limit was hit, when it resets and how long to wait. It does not wait by
default. `--wait` sleeps for the Retry-After (at most 2 minutes) and retries once. A paging-depth
refusal has no Retry-After and cannot be waited out: narrow the query.

Scripts can branch on the exit code. `po exit-codes` prints this table (`--markdown` for this form).

| exit | class | when |
|---|---|---|
| 0 | ok | success, including a warning that the SDK could not decode a response |
| 1 | unexpected | an error word or error_code not in the table (the raw body or the server's message is printed), a failure in the CLI itself |
| 2 | usage | a bad flag or argument; an unknown command; 400 refusals (id_required, id_not_accepted, invalid_cursor, unknown_sort, unknown_dir, unknown_filter, unsupported_parameter); a query the server refuses (SQL_TOO_LONG, PARSE_ERROR, MULTIPLE_STATEMENTS, every ..._NOT_ALLOWED code, SCHEMA_QUALIFIED_NAME, UNKNOWN_DATASET, UNKNOWN_COLUMN, OFFSET_TOO_LARGE, RESULT_TOO_LARGE, every INVALID_... code, UNKNOWN_FILTER_OPERATOR, SCOPE_NOT_AVAILABLE), printed with its hint and docs |
| 3 | auth | not logged in; 401 missing_token, invalid_token, expired_token (after the one silent refresh), invalid_audience; a failed login |
| 4 | forbidden | 403 access_pending, client_suspended, missing_role (the role is named), not_entitled and NOT_ENTITLED (the credit is printed) |
| 5 | not found | 404 unknown_page, unknown_section, not_found, UNKNOWN_RECORD; 410 RESULT_EXPIRED (a stored query result is gone) |
| 6 | rate limited | 429 quota_exceeded: says which limit, when it resets and how long to wait; --wait sleeps up to 2 minutes and retries once; a depth refusal cannot be waited out |
| 7 | unavailable | 503 no_identity_provider, any other 5xx, a network failure, 409 generation_changed or GENERATION_CHANGED, TIMEOUT, QUERY_FAILED |
| 8 | not answered | a 200 whose body says unbuilt or not_applicable |
| 9 | partial | --all printed some pages and then stopped |

## Updates

New API endpoints arrive automatically: the commands are generated from the SDK's API document, which
is regenerated daily.

## More

- Integration guide: <https://pharmaobservatory.com/app/integrate>
- SDKs: <https://github.com/scientist-labs/pharmaobservatory-sdks>

## Build from source

Requires Go 1.24 or newer.

```sh
go build -o po ./cmd/po
./po version
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for tests, lint and the release dry run.
