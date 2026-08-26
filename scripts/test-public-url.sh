#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=../deploy/install/public-url.sh
source "$repo_root/deploy/install/public-url.sh"

fail() {
    printf 'public URL test failed: %s\n' "$*" >&2
    exit 1
}

for value in 0.0.0.0 10.11.1.110 203.0.113.9 255.255.255.255; do
    ongrid_is_valid_ipv4 "$value" || fail "expected valid IPv4: $value"
done

for value in '' null 1.2.3 1.2.3.256 999.1.1.1 manager.example.com \
    '<?xml version="1.0"?><html><title>404-Not Found</title></html>'; do
    if ongrid_is_valid_ipv4 "$value"; then
        fail "expected invalid IPv4: $value"
    fi
done

for value in \
    https://10.11.1.110 \
    https://manager.example.com \
    http://manager.internal:8443 \
    https://localhost \
    'https://[2001:db8::1]:8443' \
    https://manager.example.com/ongrid; do
    ongrid_is_valid_public_url "$value" || fail "expected valid public URL: $value"
done

for value in \
    '' \
    10.11.1.110 \
    ftp://manager.example.com \
    https:// \
    https://https://manager.example.com \
    https://999.1.1.1 \
    https://manager.example.com: \
    https://manager.example.com:70000 \
    https://manager.example.com:18446744073709551617 \
    'https://manager.example.com/path?token=secret' \
    'https://user:password@manager.example.com' \
    'https://<?xml version="1.0"?><html><title>404-Not Found</title></html>'; do
    if ongrid_is_valid_public_url "$value"; then
        fail "expected invalid public URL: $value"
    fi
done

# Reproduce the original failure deterministically: Tencent metadata returns
# a 404 HTML body. The detector must reject it and continue to the next valid
# endpoint instead of placing the body into ONGRID_PUBLIC_URL.
curl() {
    local url=${!#}
    case "$url" in
        *metadata.tencentyun.com*)
            printf '%s' '<?xml version="1.0"?><html><title>404-Not Found</title></html>'
            ;;
        *100.100.100.200*)
            printf '%s' '198.51.100.42'
            ;;
        *)
            return 22
            ;;
    esac
}

detected=$(ongrid_detect_public_ipv4)
[[ "$detected" == 198.51.100.42 ]] || fail "expected detector to skip HTML, got: $detected"

curl() {
    printf '%s' '<html><title>404-Not Found</title></html>'
}
if ongrid_detect_public_ipv4 >/dev/null; then
    fail "detector accepted an HTML response as an IPv4 address"
fi
unset -f curl

grep -Fq 'ongrid_is_valid_public_url "$CONFIGURED_PUBLIC_URL"' \
    "$repo_root/deploy/install/install.sh" || fail "install.sh does not validate ONGRID_PUBLIC_URL"
grep -Fq 'ongrid_is_valid_public_url "$CONFIGURED_PUBLIC_URL"' \
    "$repo_root/deploy/install/upgrade.sh" || fail "upgrade.sh does not validate ONGRID_PUBLIC_URL"

printf 'public URL validation tests passed\n'
