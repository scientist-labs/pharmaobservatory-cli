# Contributing

CI runs exactly these commands (see `.github/workflows/ci.yml`).

```sh
gofmt -l .                              # must print nothing
go vet ./...
golangci-lint run                       # v2.15.0
go test -race ./...
go generate ./... && git diff --exit-code
bin/check-no-codename
goreleaser release --snapshot --clean --skip=sign   # v2.18.3, no credentials needed
```

The snapshot writes archives, `.deb` and `.rpm` packages to `dist/` (not committed).
`--skip=sign` omits the cosign step, which only runs on tagged releases.

## Internal codename rule

No file name or file content in this repo may contain the internal project codename.
`bin/check-no-codename` is a case-insensitive literal match over every tracked or
untracked, non-ignored file, with no exceptions list. CI runs it.
