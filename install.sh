#!/bin/sh
set -eu

repo="rmiyoyo/hostfix"
version="${HOSTFIX_VERSION:-latest}"
bin="$HOME/.local/bin"

case "$(uname -s)" in
    Linux) ;;
    *) echo "hostfix: only Linux servers are supported" >&2; exit 1 ;;
esac

case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "hostfix: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac

if [ "$version" = latest ]; then
    url="https://github.com/$repo/releases/latest/download/hostfix-linux-$arch"
else
    url="https://github.com/$repo/releases/download/$version/hostfix-linux-$arch"
fi

download() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1" -o "$2"
    elif command -v wget >/dev/null 2>&1; then
        wget -q "$1" -O "$2"
    else
        echo "hostfix: curl or wget is required" >&2
        exit 1
    fi
}

mkdir -p "$bin"
tmp="$(mktemp "$bin/.hostfix.XXXXXX")"
sum="$tmp.sha256"
trap 'rm -f "$tmp" "$sum"' EXIT

download "$url" "$tmp"
download "$url.sha256" "$sum"

if command -v sha256sum >/dev/null 2>&1; then
    expected="$(cut -d' ' -f1 "$sum")"
    actual="$(sha256sum "$tmp" | cut -d' ' -f1)"
    if [ "$expected" != "$actual" ]; then
        echo "hostfix: checksum mismatch, not installing" >&2
        exit 1
    fi
fi

chmod 755 "$tmp"
mv "$tmp" "$bin/hostfix"
"$bin/hostfix" shell

echo
echo "hostfix $("$bin/hostfix" version) is installed. Next:"
echo "  source ~/.bashrc"
echo "  cd ~/your-project && hostfix doctor"
