#!/usr/bin/env bash
# Private request-signing bootstrap for the pcap-parser Compose service.
# This file is sourced by install.sh and upgrade.sh; it intentionally never
# prints private key material.

ongrid_prepare_pcap_parser_auth() (
    local data_dir="$1" root manager_dir parser_dir private_key public_key tmp_public
    root="${data_dir}/pcap-parser"
    manager_dir="${root}/manager"
    parser_dir="${root}/parser"
    private_key="${manager_dir}/request.key"
    public_key="${parser_dir}/request.pub"

    command -v openssl >/dev/null 2>&1 || {
        printf '[ERROR] openssl is required to create pcap-parser request signing material\n' >&2
        return 1
    }
    install -d -m 0750 "$manager_dir" "$parser_dir"

    if [[ ! -f "$private_key" ]]; then
        openssl genpkey -algorithm ED25519 -out "$private_key" >/dev/null 2>&1 || return 1
    fi
    tmp_public=$(mktemp "${TMPDIR:-/tmp}/ongrid-pcap-parser-public.XXXXXX") || return 1
    trap 'rm -f "$tmp_public"' EXIT
    openssl pkey -in "$private_key" -pubout -out "$tmp_public" >/dev/null 2>&1 || return 1
    install -m 0644 "$tmp_public" "$public_key"

    chown -R 65532:65532 "$manager_dir"
    chown -R 10001:10001 "$parser_dir"
    chmod 0750 "$root" "$manager_dir" "$parser_dir"
    chmod 0600 "$private_key"
    chmod 0644 "$public_key"
)
