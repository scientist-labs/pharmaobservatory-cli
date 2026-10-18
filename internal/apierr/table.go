package apierr

import (
	"fmt"
	"strings"
)

// ExitClassRow is one row of the exit table.
type ExitClassRow struct {
	Code     int
	Name     string
	Triggers string
}

// ExitTable is the single source of the exit codes. `exit-codes` prints it and the README table is
// generated from it.
var ExitTable = []ExitClassRow{
	{ExitOK, "ok", "success, including a warning that the SDK could not decode a response"},
	{ExitUnexpected, "unexpected", "an error word not in the table (the raw body is printed), a failure in the CLI itself"},
	{ExitUsage, "usage", "a bad flag or argument; an unknown command; 400 refusals (id_required, id_not_accepted, invalid_cursor, unknown_sort, unknown_dir, unknown_filter, unsupported_parameter)"},
	{ExitAuth, "auth", "not logged in; 401 missing_token, invalid_token, expired_token (after the one silent refresh), invalid_audience; a failed login"},
	{ExitForbidden, "forbidden", "403 access_pending, client_suspended, missing_role (the role is named), not_entitled (the credit is printed)"},
	{ExitNotFound, "not found", "404 unknown_page, unknown_section, not_found"},
	{ExitRateLimited, "rate limited", "429 quota_exceeded: says which limit, when it resets and how long to wait; --wait sleeps up to 2 minutes and retries once; a depth refusal cannot be waited out"},
	{ExitUnavail, "unavailable", "503 no_identity_provider, any other 5xx, a network failure, 409 generation_changed"},
	{ExitNotAnswered, "not answered", "a 200 whose body says unbuilt or not_applicable"},
	{ExitPartial, "partial", "--all printed some pages and then stopped"},
}

// TableText renders ExitTable for a terminal.
func TableText() string {
	var b strings.Builder
	for _, r := range ExitTable {
		fmt.Fprintf(&b, "  %d  %-13s %s\n", r.Code, r.Name, r.Triggers)
	}
	return b.String()
}

// TableMarkdown renders ExitTable as the README table.
func TableMarkdown() string {
	var b strings.Builder
	b.WriteString("| exit | class | when |\n|---|---|---|\n")
	for _, r := range ExitTable {
		fmt.Fprintf(&b, "| %d | %s | %s |\n", r.Code, r.Name, r.Triggers)
	}
	return b.String()
}
