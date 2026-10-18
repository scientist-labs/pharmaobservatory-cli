# Packaging and release

How the CLI is built, signed and published. The release workflow is `.github/workflows/release.yml`;
nothing is published until the owner sets the repository variable `RELEASE_ENABLED` to `true` and
pushes a tag.

## Where the binary name lives

`BINARY_NAME` in the `env:` block at the top of `.goreleaser.yml`. The archives, the build output
(`po.exe` on Windows), the deb and rpm `/usr/bin` entry, the brew install and test lines, and, through
the `binaryName` ldflag, the root command's `Use`, the `version` output and the User-Agent all read it.
Verified: setting it to another value and running `goreleaser release --snapshot` yields archives,
packages, a formula and a `version` line under the new name.

A rename is one line for packaging. These are NOT derived and are a separate edit if the name changes:
the module path and `cmd/po` directory, the `PO_*` environment variables, the config directory name,
and the `po login` remedy text in `internal/apierr` and `internal/auth`. The package, formula and apt
and yum repository name is `pharmaobservatory` (the project name) and stays whatever the binary is called.

## Install instructions (draft for the README, ticket #10)

Homebrew (macOS and Linux):

    brew install scientist-labs/pharmaobservatory/pharmaobservatory

Debian and Ubuntu (apt):

    sudo install -d /usr/share/keyrings
    curl -fsSL https://packages.pharmaobservatory.com/pharmaobservatory.gpg \
      | sudo tee /usr/share/keyrings/pharmaobservatory-archive-keyring.gpg >/dev/null
    echo "deb [signed-by=/usr/share/keyrings/pharmaobservatory-archive-keyring.gpg] https://packages.pharmaobservatory.com/apt stable main" \
      | sudo tee /etc/apt/sources.list.d/pharmaobservatory.list
    sudo apt-get update && sudo apt-get install pharmaobservatory

Fedora, RHEL and derivatives (dnf or yum):

    sudo curl -fsSL -o /etc/yum.repos.d/pharmaobservatory.repo https://packages.pharmaobservatory.com/rpm/pharmaobservatory.repo
    sudo dnf install pharmaobservatory

The repository metadata is signed, the individual packages are not (`gpgcheck=0`, `repo_gpgcheck=1`).

Verify a downloaded archive (cosign keyless; replace the tag):

    cosign verify-blob \
      --certificate-identity 'https://github.com/scientist-labs/pharmaobservatory-cli/.github/workflows/release.yml@refs/tags/v0.1.0' \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com \
      --bundle checksums.txt.sigstore.json checksums.txt
    sha256sum --check --ignore-missing checksums.txt

Every archive also has its own `<archive>.sigstore.json` bundle, verified the same way.

## The apt and yum repository

`packaging/build-repos.sh <dist> <site>` builds both from a release's deb and rpm, signs the metadata
with the key in `PACKAGES_GPG_PRIVATE_KEY`, keeps the newest 5 versions per architecture, and writes the
public key (`/pharmaobservatory.gpg` binary, `/pharmaobservatory.asc` armored), `CNAME`
(`packages.pharmaobservatory.com`) and `.nojekyll`. It writes only under `<site>`. The workflow's
`packages` job pushes `<site>` to `scientist-labs/pharmaobservatory-packages` with `PACKAGES_DEPLOY_KEY`.

## The signing key

Fingerprint `DC50EED7DB9F26C8D292DD7AA7B540292B854D66`, RSA 4096, "Pharma Observatory Packages
<packages@pharmaobservatory.com>" (an identity, not a mailbox), expires 2028-10-17. The public key is
`pharmaobservatory-archive-keyring.gpg` (and `.asc`) here, and ships inside the deb and rpm.

Rotation, before the expiry or on suspected compromise:

1. Generate a new RSA 4096 key with a random passphrase in a throwaway `GNUPGHOME`.
2. Commit a keyring holding BOTH public keys (`gpg --export OLD NEW`) and update the fingerprint above.
3. Update `PACKAGES_GPG_PRIVATE_KEY` and `PACKAGES_GPG_PASSPHRASE` to the NEW key. `build-repos.sh`
   signs with the one secret key it is given and requires its fingerprint to be in the committed
   keyring; to sign with both for an overlap release, import both secret keys and sign with
   `--local-user` for each (a small change to `sign()`, not needed until the first rotation).
4. Release once so installed users receive the two-key keyring through the package, then drop the old key.
5. On compromise, also publish the revocation certificate and tell users to re-run the apt or dnf key line.

## Release day checklist

1. Settle the binary name (`BINARY_NAME`), merge the rename if it changed.
2. Confirm DNS for `packages.pharmaobservatory.com` (scientist-hq/infra#887) and domain verification in Pages.
3. Enable GitHub Pages on `scientist-labs/pharmaobservatory-packages`: branch `main`, root.
4. Set the repository variable: `gh variable set RELEASE_ENABLED --body true --repo scientist-labs/pharmaobservatory-cli`.
5. Tag and push: `git tag v0.1.0 && git push origin v0.1.0` (the first release needs a tagged SDK, see issue #1).
6. Watch the `Release` run: `release` then `packages`. Then check the install lines above on a clean machine.
7. Unset `RELEASE_ENABLED` again if releases should stay manual (regenerate.yml tags a patch release unattended while it is set).
