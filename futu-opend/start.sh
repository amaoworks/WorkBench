#!/bin/bash
set -eu

OPEND_DIR=${FUTU_OPEND_DIR:-/opt/FutuOpenD}
DATA_DIR=${FUTU_OPEND_DATA:-/home/futu/.com.futunn.FutuOpenD}
VER=${FUTU_OPEND_VER:-10.10.7008}
IP=${FUTU_OPEND_IP:-0.0.0.0}
PORT=${FUTU_OPEND_PORT:-11111}
TELNET_PORT=${FUTU_OPEND_TELNET_PORT:-22222}
LANG=${FUTU_LANG:-chs}
LOG_LEVEL=${FUTU_LOG_LEVEL:-info}

if [ -z "${FUTU_ACCOUNT_ID:-}" ]; then
    echo "FUTU_ACCOUNT_ID is required" >&2
    exit 1
fi
if [ -z "${FUTU_ACCOUNT_PWD_MD5:-}" ]; then
    echo "FUTU_ACCOUNT_PWD_MD5 is required (32-char hex MD5 of the password)" >&2
    exit 1
fi
case "$FUTU_ACCOUNT_PWD_MD5" in
    *[!0-9a-fA-F]* | "")
        echo "FUTU_ACCOUNT_PWD_MD5 must be 32 hex characters" >&2
        exit 1
        ;;
esac
if [ "${#FUTU_ACCOUNT_PWD_MD5}" -ne 32 ]; then
    echo "FUTU_ACCOUNT_PWD_MD5 must be 32 hex characters" >&2
    exit 1
fi

mkdir -p "$OPEND_DIR" "$DATA_DIR"
cd "$OPEND_DIR"

if [ ! -x "$OPEND_DIR/FutuOpenD" ]; then
    echo "OpenD binary missing; downloading $VER"
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    downloaded=
    for dist in Ubuntu16.04 Ubuntu18.04; do
        name="Futu_OpenD_${VER}_${dist}.tar.gz"
        url="https://softwaredownload.futunn.com/${name}"
        echo "trying $url"
        if curl -fsSL --retry 3 -A "Mozilla/5.0" -o "$tmp/$name" "$url"; then
            tar -xzf "$tmp/$name" -C "$tmp"
            found=$(find "$tmp" -type f -name FutuOpenD | head -n1)
            if [ -n "$found" ]; then
                cp -a "$(dirname "$found")/." "$OPEND_DIR/"
                downloaded=1
                break
            fi
        fi
    done
    if [ -z "$downloaded" ]; then
        echo "failed to download FutuOpenD $VER" >&2
        exit 1
    fi
    chmod +x "$OPEND_DIR/FutuOpenD"
    rm -rf "$tmp"
    trap - EXIT
fi

cfg=$(mktemp)
sed \
    -e "s|<ip>.*</ip>|<ip>${IP}</ip>|" \
    -e "s|<api_port>.*</api_port>|<api_port>${PORT}</api_port>|" \
    -e "s|<telnet_ip>.*</telnet_ip>|<telnet_ip>${IP}</telnet_ip>|" \
    -e "s|<telnet_port>.*</telnet_port>|<telnet_port>${TELNET_PORT}</telnet_port>|" \
    -e "s|<login_account>.*</login_account>|<login_account>${FUTU_ACCOUNT_ID}</login_account>|" \
    -e "s|<login_pwd_md5>.*</login_pwd_md5>|<login_pwd_md5>${FUTU_ACCOUNT_PWD_MD5}</login_pwd_md5>|" \
    -e "s|<lang>.*</lang>|<lang>${LANG}</lang>|" \
    -e "s|<log_level>.*</log_level>|<log_level>${LOG_LEVEL}</log_level>|" \
    /opt/template/FutuOpenD.xml.template > "$cfg"

echo "starting FutuOpenD on ${IP}:${PORT} (telnet ${TELNET_PORT} for 2FA if prompted)"
exec "$OPEND_DIR/FutuOpenD" -cfg_file="$cfg"
