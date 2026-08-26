#!/usr/bin/env bash
# Shared ONGRID_PUBLIC_URL validation and public IPv4 detection helpers.
# Keep this file compatible with Bash 4.2 (the default on CentOS 7).

ongrid_is_valid_ipv4() {
    local value="${1:-}" octet
    local -a octets

    [[ "$value" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
    IFS=. read -r -a octets <<<"$value"
    [[ ${#octets[@]} -eq 4 ]] || return 1

    for octet in "${octets[@]}"; do
        (( 10#$octet <= 255 )) || return 1
    done
    return 0
}

ongrid_fetch_public_ipv4() {
    local url="$1" timeout_seconds="${2:-3}" candidate

    # -f rejects HTTP 4xx/5xx bodies. The format check below also rejects
    # proxies or metadata services that return an HTML error with status 200.
    candidate=$(curl -fsS --max-time "$timeout_seconds" "$url" 2>/dev/null || true)
    candidate=$(printf '%s' "$candidate" | tr -d '[:space:]')
    ongrid_is_valid_ipv4 "$candidate" || return 1
    printf '%s' "$candidate"
}

ongrid_detect_public_ipv4() {
    local timeout_seconds url candidate

    while IFS='|' read -r timeout_seconds url; do
        candidate=$(ongrid_fetch_public_ipv4 "$url" "$timeout_seconds" || true)
        if [[ -n "$candidate" ]]; then
            printf '%s' "$candidate"
            return 0
        fi
    done <<'EOF'
2|http://metadata.tencentyun.com/latest/meta-data/public-ipv4
2|http://100.100.100.200/latest/meta-data/eipv4
2|http://169.254.169.254/latest/meta-data/public-ipv4
3|https://api.ipify.org
3|https://ifconfig.me
EOF

    return 1
}

ongrid_is_valid_public_url() {
    local value="${1:-}" rest authority host port="" label
    local -a labels

    case "$value" in
        http://*)  rest=${value#http://} ;;
        https://*) rest=${value#https://} ;;
        *) return 1 ;;
    esac

    # PublicURL is later concatenated with fixed data-plane paths. Reject
    # whitespace, HTML, fragments, queries and credentials up front.
    [[ -n "$rest" ]] || return 1
    [[ "$value" != *[[:space:]]* ]] || return 1
    [[ "$value" != *'<'* && "$value" != *'>'* ]] || return 1
    [[ "$value" != *'"'* && "$value" != *"'"* && "$value" != *'`'* ]] || return 1
    [[ "$value" != *'?'* && "$value" != *'#'* ]] || return 1

    authority=${rest%%/*}
    [[ -n "$authority" && "$authority" != *'@'* ]] || return 1

    if [[ "$authority" == \[* ]]; then
        [[ "$authority" =~ ^\[([0-9A-Fa-f:.]+)\](:([0-9]{1,5}))?$ ]] || return 1
        host=${BASH_REMATCH[1]}
        port=${BASH_REMATCH[3]:-}
        [[ "$host" == *:* ]] || return 1
    else
        [[ "$authority" != *:*:* ]] || return 1
        if [[ "$authority" == *:* ]]; then
            host=${authority%%:*}
            port=${authority##*:}
            [[ -n "$port" ]] || return 1
        else
            host=$authority
        fi

        [[ -n "$host" ]] || return 1
        if [[ "$host" =~ ^[0-9.]+$ ]]; then
            ongrid_is_valid_ipv4 "$host" || return 1
        else
            [[ "$host" != .* && "$host" != *. && "$host" != *..* ]] || return 1
            IFS=. read -r -a labels <<<"$host"
            for label in "${labels[@]}"; do
                [[ "$label" =~ ^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?$ ]] || return 1
            done
        fi
    fi

    if [[ -n "$port" ]]; then
        [[ "$port" =~ ^[0-9]{1,5}$ ]] || return 1
        (( 10#$port >= 1 && 10#$port <= 65535 )) || return 1
    fi
    return 0
}
