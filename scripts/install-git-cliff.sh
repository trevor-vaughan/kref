#!/usr/bin/env bash
# Installs the pinned git-cliff into <dest>/git-cliff after checking the release tarball's
# SHA-512 against the digest pinned here (not the .sha512 file shipped beside it, which comes
# from the same release). Usage: install-git-cliff.sh <dest-dir>
set -euo pipefail

VERSION=2.14.2

dest="${1:?usage: install-git-cliff.sh <dest-dir>}"

os="$(uname -s)"
arch="$(uname -m)"

# A case, not an associative array: macOS ships bash 3.2.
case "${os}-${arch}" in
  Linux-x86_64)
    triple=x86_64-unknown-linux-gnu
    sha512=26d1f7c8ea2400f2ccb0b2e7f321635f600b583bf4cd1f7afaa5e8c24068ad1088a2504e0a4d9f4333d4bb20a80329327d9462634343ee3af91e1a7fbdc6f18a
    ;;
  Linux-aarch64 | Linux-arm64)
    triple=aarch64-unknown-linux-gnu
    sha512=fd27768ea76fea8c3ba3f3247fef2f6604988b629f15e48bf40be5eaa08b8fef2e4087cc9c8af16bc522bc19cc369611fa7692101b513f63469ad58522708600
    ;;
  Darwin-x86_64)
    triple=x86_64-apple-darwin
    sha512=4d32c50a010cbff65e46f1f36347c2dd1a20f17efea7b77115d3166146ad467dc803cd07b839fc35e0a6639aa8d1af9eab7cf8f7984bcd819513f46a396ff801
    ;;
  Darwin-arm64)
    triple=aarch64-apple-darwin
    sha512=6ec30660233fcd73b92e9d888965945b6435d6fcd7120df8d7c0799faa46cf06d563d49de721ccf4387e6d61bdc5b37c8eb73273c7d9a0883b90cd9857c6ad9f
    ;;
  *)
    echo "no pinned git-cliff ${VERSION} for ${os}-${arch}; install it yourself: cargo install --locked git-cliff --version ${VERSION}" >&2
    exit 1
    ;;
esac

if command -v sha512sum >/dev/null; then
  verify=(sha512sum -c -)
else
  verify=(shasum -a 512 -c -)
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
archive="git-cliff-${VERSION}-${triple}.tar.gz"
curl -fsSL -o "${work}/${archive}" "https://github.com/orhun/git-cliff/releases/download/v${VERSION}/${archive}"
(cd "$work" && echo "${sha512}  ${archive}" | "${verify[@]}" >/dev/null)
tar -xzf "${work}/${archive}" -C "$work"
mkdir -p "$dest"
install -m 0755 "${work}/git-cliff-${VERSION}/git-cliff" "${dest}/git-cliff"
"${dest}/git-cliff" --version >&2
