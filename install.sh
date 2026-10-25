#!/bin/sh
# Installs the latest muse-cost release into ~/.local/bin (or $MUSE_COST_INSTALL_DIR):
#   curl -fsSL https://raw.githubusercontent.com/omenking/meta-costcenter/main/install.sh | sh
set -eu

repo=omenking/meta-costcenter
dir=${MUSE_COST_INSTALL_DIR:-$HOME/.local/bin}

case "$(uname -s)" in
  Linux) ;;
  *) echo "muse-cost runs on Linux (including WSL); this system is $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "No muse-cost release for $(uname -m)" >&2; exit 1 ;;
esac

archive=muse-cost_linux_$arch.tar.gz
base=https://github.com/$repo/releases/latest/download
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive"
curl -fsSL "$base/$archive" -o "$tmp/$archive"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
(cd "$tmp" && grep " $archive\$" checksums.txt | sha256sum -c --quiet -)
tar -xzf "$tmp/$archive" -C "$tmp" muse-cost

mkdir -p "$dir"
install -m 0755 "$tmp/muse-cost" "$dir/muse-cost"
echo "Installed $("$dir/muse-cost" --version) to $dir/muse-cost"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, for example: echo 'export PATH=\"$dir:\$PATH\"' >> ~/.bashrc" ;;
esac
echo "Next: run 'muse-cost show' to open the dashboard."
