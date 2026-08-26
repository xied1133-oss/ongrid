#!/usr/bin/env bash
# ==============================================================================
# ongrid 一键部署脚本（阿里云 ECS / 任意 Linux，已装 Docker 社区版）
#
# 用法（在 ECS 上以 root 或 sudo 用户执行）：
#   sudo ./one-click-deploy.sh
#
# 常用可覆盖变量（执行前通过环境变量传入）：
#   ONGRID_VERSION      发布版本，默认 v0.13.4
#   ONGRID_DL_BASE_URL  下载源，默认官方 CDN（国内快）；可换 GitHub release 地址
#   HTTP_PORT           HTTPS 对外端口（默认沿用发布包配置）
#   ADMIN_EMAIL         管理员邮箱（默认 admin@ongrid.local）
#   ADMIN_PASSWORD      管理员初始密码（留空 = install.sh 自动生成随机密码）
#   OPENAI_API_KEY      模型 key（留空则 AI Chat 不可用，其余功能正常）
#   OPENAI_MODEL        模型名（可选）
#   OPENAI_BASE_URL     自定义 OpenAI 兼容 endpoint（可选，如国内模型网关）
#
# 示例：
#   sudo OPENAI_API_KEY=sk-xxx ADMIN_EMAIL=ops@corp.com ./one-click-deploy.sh
# ==============================================================================
set -Eeuo pipefail

VERSION="${ONGRID_VERSION:-v0.13.4}"
DL_BASE="${ONGRID_DL_BASE_URL:-https://ongrid.cloud/dl}"
WORKDIR="/tmp/ongrid-onestep.$$"

log()  { printf '\033[1;32m[deploy]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; exit 1; }
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

# --- 0. root 权限（非 root 自动 sudo 重入，透传环境变量） -------------------
if [[ $EUID -ne 0 ]]; then
  log "非 root 用户，使用 sudo 重新执行..."
  exec sudo -E bash "$0" "$@"
fi

# --- 1. 环境自检 -------------------------------------------------------------
log "检查运行环境..."
[[ "$(uname -s)" == "Linux" ]] || die "仅支持 Linux 服务器"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  *) die "不支持的架构: $ARCH（仅支持 amd64 / arm64）" ;;
esac

command -v docker >/dev/null 2>&1 || die "未检测到 docker，请先安装 Docker"
docker info >/dev/null 2>&1       || die "Docker daemon 未运行，执行: systemctl start docker"
docker compose version >/dev/null 2>&1 || die "缺少 Docker Compose v2（docker compose 子命令）"
log "Docker $(docker --version | awk '{print $3}') / $(docker compose version --short) ✓  架构 linux-${GOARCH}"

# 内存不足 2GB 给警告，不阻断
if command -v free >/dev/null 2>&1; then
  MEM_MB=$(free -m | awk '/^Mem:/ {print $2}')
  [[ "$MEM_MB" -lt 1900 ]] && warn "内存 ${MEM_MB}MB 低于建议值 2GB，全栈运行可能吃力"
fi

# 下载工具
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "需要 curl 或 wget"
fi

# --- 2. 下载发布包 -----------------------------------------------------------
mkdir -p "$WORKDIR"
TARBALL="ongrid-${VERSION}-linux-${GOARCH}.tar.xz"
URL="${DL_BASE%/}/${TARBALL}"

log "下载发布包: $URL"
if ! fetch "$URL" "$WORKDIR/$TARBALL"; then
  # CDN 失败自动回退 GitHub Release
  URL="https://github.com/ongridio/ongrid/releases/download/${VERSION}/${TARBALL}"
  warn "首选下载源失败，回退 GitHub: $URL"
  fetch "$URL" "$WORKDIR/$TARBALL" || die "发布包下载失败，请检查网络或版本号（当前: $VERSION）"
fi

# 校验 SHA-256（发布源提供 .sha256 时强校验，否则仅警告）
if fetch "${URL}.sha256" "$WORKDIR/$TARBALL.sha256" 2>/dev/null; then
  log "校验 SHA-256..."
  ACTUAL_SHA=$(sha256sum "$WORKDIR/$TARBALL" | awk '{print $1}')
  grep -q "$ACTUAL_SHA" "$WORKDIR/$TARBALL.sha256" \
    || die "SHA-256 校验失败，发布包可能损坏"
  log "SHA-256 ✓"
else
  warn "下载源未提供 .sha256，跳过校验"
fi

# --- 3. 解压并预填配置 -------------------------------------------------------
log "解压发布包..."
tar -xf "$WORKDIR/$TARBALL" -C "$WORKDIR"
PKG_DIR="$WORKDIR/ongrid-${VERSION}-linux"
[[ -d "$PKG_DIR" ]] || PKG_DIR="$WORKDIR/$(basename "$TARBALL" .tar.xz)"
[[ -d "$PKG_DIR" ]] || die "解压后未找到发布包目录"
cd "$PKG_DIR"
[[ -x ./install.sh ]] || die "发布包缺少 install.sh"

# 把传入的变量写进 .env.example（install.sh 会拷贝为 .env，空值自动生成随机密钥）
set_env() { # set_env KEY VALUE —— 删除旧行后追加，避免 sed 转义问题
  sed -i "/^$1=/d" .env.example
  printf '%s=%s\n' "$1" "$2" >> .env.example
}

[[ -n "${ADMIN_EMAIL:-}"     ]] && set_env ONGRID_ADMIN_EMAIL    "$ADMIN_EMAIL"
[[ -n "${ADMIN_PASSWORD:-}"  ]] && set_env ONGRID_ADMIN_PASSWORD "$ADMIN_PASSWORD"
[[ -n "${HTTP_PORT:-}"       ]] && set_env ONGRID_HTTP_PORT      "$HTTP_PORT"
[[ -n "${OPENAI_API_KEY:-}"  ]] && set_env OPENAI_API_KEY        "$OPENAI_API_KEY"
[[ -n "${OPENAI_MODEL:-}"    ]] && set_env OPENAI_MODEL          "$OPENAI_MODEL"
[[ -n "${OPENAI_BASE_URL:-}" ]] && set_env OPENAI_BASE_URL       "$OPENAI_BASE_URL"

# --- 4. 执行官方安装 ---------------------------------------------------------
log "运行 install.sh（拉取镜像 + 启动全栈，可能需要几分钟）..."
./install.sh

# --- 5. 部署后验证 -----------------------------------------------------------
HTTP_PORT_ACTUAL=$(grep '^ONGRID_HTTP_PORT=' /opt/ongrid/.env 2>/dev/null | cut -d= -f2- || true)
HTTP_PORT_ACTUAL="${HTTP_PORT_ACTUAL:-8443}"

log "验证 /healthz ..."
for _ in $(seq 1 12); do
  curl -kfsS "https://localhost:${HTTP_PORT_ACTUAL}/healthz" >/dev/null 2>&1 && break
  sleep 5
done
curl -kfsS "https://localhost:${HTTP_PORT_ACTUAL}/healthz" >/dev/null 2>&1 \
  || warn "healthz 暂未就绪，可稍后执行: curl -k https://localhost:${HTTP_PORT_ACTUAL}/healthz"

PUBLIC_IP=$(curl -s --max-time 5 http://100.100.100.200/latest/meta-data/eipv4 2>/dev/null || true)
[[ -z "$PUBLIC_IP" ]] && PUBLIC_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[[ -z "$PUBLIC_IP" ]] && PUBLIC_IP="<服务器IP>"

cat <<EOF

==============================================================
 ✅ ongrid ${VERSION} 部署完成
--------------------------------------------------------------
 Web UI   : https://${PUBLIC_IP}:${HTTP_PORT_ACTUAL}/
 Grafana  : https://${PUBLIC_IP}:${HTTP_PORT_ACTUAL}/grafana/
 Edge 隧道: ${PUBLIC_IP}:40012
--------------------------------------------------------------
 ⚠️  管理员初始密码在上方 install.sh 输出末尾，只显示一次！
     （也保存在 /opt/ongrid/.env 的 ONGRID_ADMIN_PASSWORD）
 ⚠️  阿里云安全组请确认已放行: ${HTTP_PORT_ACTUAL}/tcp、40012/tcp
 ⚠️  自签证书浏览器会告警，点「高级 → 继续访问」即可
==============================================================
EOF
