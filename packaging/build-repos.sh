#!/usr/bin/env bash
# Build the signed apt and yum repositories for the GitHub Pages site served at
# https://packages.pharmaobservatory.com (repo scientist-labs/pharmaobservatory-packages).
#
#   packaging/build-repos.sh <dist-dir> <site-dir>
#
# <dist-dir>  holds the release's pharmaobservatory_*.deb and pharmaobservatory_*.rpm (GoReleaser dist/).
# <site-dir>  is the working tree of the packages repo (or any directory for a dry run). Existing
#             content is kept, the new packages are added, metadata is regenerated, and only the
#             newest KEEP_VERSIONS versions per package kind and architecture are retained.
#
# Signing key, from the environment, never printed:
#   GPG_PRIVATE_KEY   armored private key (repo secret PACKAGES_GPG_PRIVATE_KEY)
#   GPG_PASSPHRASE    its passphrase (repo secret PACKAGES_GPG_PASSPHRASE)
# The key is imported into a throwaway GNUPGHOME that is removed on exit.
#
# Needs: apt-ftparchive (apt-utils), createrepo_c (createrepo-c), gpg, gzip.
# This script only writes under <site-dir>. It never pushes; publishing is the workflow's job.
set -euo pipefail

dist=${1:?usage: build-repos.sh <dist-dir> <site-dir>}
site=${2:?usage: build-repos.sh <dist-dir> <site-dir>}
here=$(cd "$(dirname "$0")" && pwd)
host=${PACKAGES_HOST:-packages.pharmaobservatory.com}
keep=${KEEP_VERSIONS:-5}
: "${GPG_PRIVATE_KEY:?GPG_PRIVATE_KEY is not set}"
: "${GPG_PASSPHRASE:?GPG_PASSPHRASE is not set}"

shopt -s nullglob
debs=("$dist"/pharmaobservatory_*.deb)
rpms=("$dist"/pharmaobservatory_*.rpm)
[ ${#debs[@]} -gt 0 ] && [ ${#rpms[@]} -gt 0 ] || { echo "no deb or rpm files in $dist" >&2; exit 1; }

GNUPGHOME=$(mktemp -d)
export GNUPGHOME
chmod 700 "$GNUPGHOME"
trap 'rm -rf "$GNUPGHOME"' EXIT
printf '%s\n' "$GPG_PRIVATE_KEY" | gpg --batch --quiet --pinentry-mode loopback --passphrase-fd 3 --import 3<<<"$GPG_PASSPHRASE"
fpr=$(gpg --batch --with-colons --list-secret-keys | awk -F: '/^fpr/{print $10; exit}')
[ -n "$fpr" ] || { echo "no secret key imported" >&2; exit 1; }
sign() { gpg --batch --yes --quiet --pinentry-mode loopback --passphrase-fd 3 --local-user "$fpr" "$@" 3<<<"$GPG_PASSPHRASE"; }

# The published public key must be the one that signs; a mismatch would ship a repository nobody can verify.
want=$(gpg --batch --with-colons --show-keys "$here/pharmaobservatory-archive-keyring.asc" | awk -F: '/^fpr/{print $10; exit}')
[ "$want" = "$fpr" ] || { echo "signing key $fpr is not the committed public key $want" >&2; exit 1; }

# prune <dir> <glob-suffix>: keep the newest $keep files of each architecture (version sort).
prune() {
  local dir=$1 ext=$2 arch
  for arch in amd64 arm64; do
    ls -1 "$dir" 2>/dev/null | grep -E "_linux_${arch}\.${ext}\$" | sort -V | head -n "-$keep" | while read -r f; do rm -f "$dir/$f"; done
  done
}

# ---- apt: apt/pool/main + apt/dists/stable ----
apt="$site/apt"
mkdir -p "$apt/pool/main"
cp "${debs[@]}" "$apt/pool/main/"
prune "$apt/pool/main" deb
rm -rf "$apt/dists"
for arch in amd64 arm64; do
  d="$apt/dists/stable/main/binary-$arch"
  mkdir -p "$d"
  (cd "$apt" && apt-ftparchive --arch "$arch" packages pool/main > "dists/stable/main/binary-$arch/Packages")
  gzip -9nk "$d/Packages"
done
apt-ftparchive \
  -o APT::FTPArchive::Release::Origin="Pharma Observatory" \
  -o APT::FTPArchive::Release::Label="Pharma Observatory" \
  -o APT::FTPArchive::Release::Suite=stable \
  -o APT::FTPArchive::Release::Codename=stable \
  -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
  -o APT::FTPArchive::Release::Components=main \
  release "$apt/dists/stable" > "$apt/dists/stable/Release"
sign --clearsign --output "$apt/dists/stable/InRelease" "$apt/dists/stable/Release"
sign --armor --detach-sign --output "$apt/dists/stable/Release.gpg" "$apt/dists/stable/Release"

# ---- yum: rpm/ (flat, both architectures; dnf filters on $basearch) ----
rpmdir="$site/rpm"
mkdir -p "$rpmdir"
cp "${rpms[@]}" "$rpmdir/"
prune "$rpmdir" rpm
rm -rf "$rpmdir/repodata"
createrepo_c --quiet "$rpmdir"
sign --armor --detach-sign --output "$rpmdir/repodata/repomd.xml.asc" "$rpmdir/repodata/repomd.xml"
# Packages are not individually signed (design note, issue #1): gpgcheck=0, repo_gpgcheck=1.
cat > "$rpmdir/pharmaobservatory.repo" <<REPO
[pharmaobservatory]
name=Pharma Observatory
baseurl=https://$host/rpm
enabled=1
gpgcheck=0
repo_gpgcheck=1
gpgkey=https://$host/pharmaobservatory.asc
REPO

# ---- site root: public key at a stable URL, Pages files ----
cp "$here/pharmaobservatory-archive-keyring.gpg" "$site/pharmaobservatory.gpg"
cp "$here/pharmaobservatory-archive-keyring.asc" "$site/pharmaobservatory.asc"
printf '%s\n' "$host" > "$site/CNAME"
: > "$site/.nojekyll"
echo "built apt and yum repositories under $site (signing key $fpr)"
