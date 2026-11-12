#!/usr/bin/env bash
# Ubuntu installer: web panel, bundled data and optional Telegram activation.
set -Eeuo pipefail
umask 022

REPOSITORY=https://github.com/Haj-amirreza/tg_go.git
BRANCH=main
RELEASES=/opt/unibot/releases
CONFIG=/etc/unibot-web/web.env
DOMAIN=
REVISION=
CHECK=0
BOOTSTRAP=1
REPLACE_PROXY=0
WITH_BOT=0
OWNER_ID=
EXPECTED_BOT=
ADMIN_IDS=()

die() { printf '%s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'TEXT'
Usage: sudo bash install-ubuntu.sh --domain panel.example.org [--ref COMMIT_SHA] [--skip-bootstrap]
       bash install-ubuntu.sh --domain panel.example.org --check
Ubuntu 22.04/24.04, amd64/arm64. Fresh web-only installation; existing credentials and volumes are preserved.
--check           Read-only local preflight; no downloads, package installation or service changes.
--ref             Pin a published 40-character commit from the main branch (default: resolve branch head).
--skip-bootstrap  Leave first-admin creation for a separate interactive command.
--replace-proxy   Stop the previous owner of TCP 80/443 and restore it if new proxy startup fails.
--with-bot        Also back up/import bundled data, request private bot credentials and activate Telegram.
--owner-id ID --admin-id ID --expected-bot USERNAME  Pin owner, additional admin(s) and bot identity.
For private repositories, configure Git read access for the root/sudo user first.
DNS must already point to this VPS, with inbound TCP 80/443 open. SSH/UFW and provider firewall settings are not edited.
TEXT
}
while (($#)); do
  case "$1" in
    --domain|--ref)
      (($# >= 2)) || die "Missing value for $1."
      if [[ "$1" == --domain ]]; then DOMAIN=${2,,}; else REVISION=${2,,}; fi
      shift 2 ;;
    --owner-id|--admin-id|--expected-bot)
      (($# >= 2)) || die "Missing value for $1."
      if [[ "$1" == --expected-bot ]]; then
        EXPECTED_BOT=${2#@}; [[ "$EXPECTED_BOT" =~ ^[A-Za-z][A-Za-z0-9_]{4,31}$ ]] || die 'Invalid bot username.'
      else
        [[ "$2" =~ ^[1-9][0-9]{0,18}$ ]] || die 'A numeric Telegram ID is required.'
        [[ ${#2} -lt 19 || "$2" < 9223372036854775808 ]] || die 'Telegram ID exceeds the supported integer range.'
        if [[ "$1" == --owner-id ]]; then OWNER_ID=$2; else ADMIN_IDS+=(--admin-id "$2"); fi
      fi
      shift 2 ;;
    --check) CHECK=1; shift ;;
    --skip-bootstrap) BOOTSTRAP=0; shift ;;
    --replace-proxy) REPLACE_PROXY=1; shift ;;
    --with-bot) WITH_BOT=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) die 'Unknown argument; use --help.' ;;
  esac
done
[[ -n "$DOMAIN" && ${#DOMAIN} -le 253 && "$DOMAIN" =~ ^[a-z0-9.-]+$ && "$DOMAIN" == *.* && "$DOMAIN" != .* && "$DOMAIN" != *. ]] || die 'A public DNS hostname is required.'
IFS=. read -r -a labels <<< "$DOMAIN"
[[ "${labels[-1]}" =~ ^[a-z]{2,63}$ && "$DOMAIN" != *.localhost ]] || die 'Use a public DNS hostname, without a URL, port or path.'
for label in "${labels[@]}"; do
  [[ "$label" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || die 'Invalid DNS hostname.'
done
[[ -z "$REVISION" || "$REVISION" =~ ^[a-f0-9]{40}$ ]] || die '--ref requires a full 40-character commit SHA.'
[[ -r /etc/os-release ]] || die 'Cannot identify the operating system.'
# This is the root-owned operating-system metadata, never a user-provided config.
. /etc/os-release
[[ "$ID" == ubuntu ]] || die 'Ubuntu is required.'
case "$VERSION_ID" in 22.04) codename=jammy ;; 24.04) codename=noble ;; *) die 'Use Ubuntu 22.04 or 24.04.' ;; esac
architecture=$(dpkg --print-architecture)
[[ "$architecture" == amd64 || "$architecture" == arm64 ]] || die 'Use an amd64 or arm64 VPS.'
unset DOCKER_HOST DOCKER_CONTEXT DOCKER_TLS DOCKER_TLS_VERIFY DOCKER_CERT_PATH
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE

# Refuse a mismatched or unprotected existing config before any installation work.
if [[ -e "$CONFIG" || -L "$CONFIG" ]]; then
  command -v python3 >/dev/null || die 'Existing config preserved; Python 3 is needed to inspect it.'
  python3 - "$CONFIG" "$DOMAIN" "$REVISION" "$WITH_BOT" <<'PY'
import os, re, stat, sys
from pathlib import Path
p=Path(sys.argv[1])
try: s=p.lstat()
except OSError: raise SystemExit('Cannot inspect the private config; rerun the preflight with sudo.')
if not stat.S_ISREG(s.st_mode) or s.st_mode & 0o077 or s.st_uid != os.geteuid() or s.st_nlink != 1:
    raise SystemExit('Existing config must be a private regular file owned by the installer user; nothing changed.')
try:
    entries=[line.split('=',1) for line in p.read_text().splitlines() if line and not line.startswith('#')]
    values=dict(entries)
    assert len(entries)==len(values) and set(values)=={'APP_DOMAIN','APP_ORIGIN','APP_IMAGE','POSTGRES_PASSWORD','APP_DB_PASSWORD','APP_KEY'}
    assert values['APP_DOMAIN']==sys.argv[2] and values['APP_ORIGIN']=='https://'+sys.argv[2]
    for key,length in [('POSTGRES_PASSWORD',48),('APP_DB_PASSWORD',48),('APP_KEY',64)]:
        assert re.fullmatch('[0-9a-f]{'+str(length)+'}',values[key])
    assert len({values['POSTGRES_PASSWORD'],values['APP_DB_PASSWORD'],values['APP_KEY']})==3
    assert re.fullmatch(r'unibot-next:web-[a-z0-9][a-z0-9_.-]{0,79}',values['APP_IMAGE'])
    if sys.argv[3] and sys.argv[4]!='1': assert values['APP_IMAGE']=='unibot-next:web-'+sys.argv[3]
except (OSError, ValueError, AssertionError):
    raise SystemExit('Existing config/domain was preserved; use the upgrade guide rather than a new installation.')
PY
fi
if [[ -e "$CONFIG" && -z "$REVISION" ]]; then
  die 'Existing config preserved. Resume with the original full --ref SHA, or use the backup/upgrade guide.'
fi
available=$(df -Pk /opt | awk 'NR==2 {print $4}')
[[ "$available" =~ ^[0-9]+$ && "$available" -ge 15728640 ]] || die 'At least 15 GiB free under /opt is required for image builds.'
if ((CHECK)); then
  printf 'Preflight passed: Ubuntu %s / %s, web-only, domain %s. No host changes made.\n' "$VERSION_ID" "$architecture" "$DOMAIN"
  printf 'Ensure DNS and external TCP 80/443 are ready; install will preserve SSH/UFW and provider firewall settings.\n'
  exit 0
fi
((EUID == 0)) || die 'Run with sudo; --check is available without installation privileges.'
if ((WITH_BOT)) && [[ ! -t 0 ]]; then
  die '--with-bot requires an interactive terminal for private Telegram credentials.'
fi
if ((BOOTSTRAP)) && [[ ! -e "$CONFIG" && ! -t 0 ]]; then
  die 'First-admin setup requires an interactive terminal; use --skip-bootstrap for a headless installation.'
fi
# Preserve an existing reverse proxy instead of discovering the conflict after a build.
if command -v ss >/dev/null && [[ -n "$(ss -H -ltn '( sport = :80 or sport = :443 )')" ]]; then
  own_proxy=
  if command -v docker >/dev/null; then
    own_proxy=$(docker --host unix:///var/run/docker.sock ps --filter label=com.docker.compose.project=unibot-web --filter label=com.docker.compose.service=caddy --format '{{.ID}}' 2>/dev/null || true)
  fi
  [[ -n "$own_proxy" || "$REPLACE_PROXY" == 1 ]] || die 'Host TCP 80/443 is occupied. Inspect sudo ss -ltnp or use --replace-proxy to replace the previous web proxy.'
fi
command -v git >/dev/null || die 'Install Git and configure private GitHub access before installation; see the guide.'
if ! remote=$(GIT_TERMINAL_PROMPT=0 git ls-remote --exit-code "$REPOSITORY" "refs/heads/$BRANCH" 2>/dev/null); then
  die 'Cannot read the repository; check network and root/sudo GitHub access. For a private repository run sudo gh auth login --hostname github.com --git-protocol https --web, then sudo gh auth setup-git --hostname github.com.'
fi
if [[ -z "$REVISION" ]]; then
  REVISION=${remote%%[[:space:]]*}
  [[ "$REVISION" =~ ^[a-f0-9]{40}$ ]] || die 'Cannot resolve the published rewrite branch.'
fi
trap 'printf "Installation stopped. Existing credentials/data were preserved. Fix the failing step and rerun the same --ref; see docs/UBUNTU_INSTALL_FA.md.\n" >&2' ERR

if ! command -v docker >/dev/null; then
  for path in /etc/apt/keyrings/docker.asc /etc/apt/sources.list.d/docker.sources /etc/apt/sources.list.d/docker.list; do
    [[ ! -e "$path" && ! -L "$path" ]] || die 'Existing Docker repository settings preserved; finish or inspect that installation first.'
  done
  for package in docker.io docker-compose docker-compose-v2 podman-docker containerd runc; do
    if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -q 'install ok installed'; then
      die 'Existing container packages preserved; inspect Docker installation before continuing.'
    fi
  done
  apt-get update
  apt-get install -y ca-certificates curl git python3
  install -d -m 0755 /etc/apt/keyrings
  key_tmp=$(mktemp /etc/apt/keyrings/.unibot-docker.XXXXXX)
  trap 'rm -f -- "$key_tmp"' EXIT
  curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
    https://download.docker.com/linux/ubuntu/gpg -o "$key_tmp"
  chmod 0644 "$key_tmp"
  ln "$key_tmp" /etc/apt/keyrings/docker.asc
  (
    set -C
    cat > /etc/apt/sources.list.d/docker.sources <<EOF
Types: deb
URIs: https://download.docker.com/linux/ubuntu
Suites: $codename
Components: stable
Architectures: $architecture
Signed-By: /etc/apt/keyrings/docker.asc
EOF
  )
  apt-get update
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
else
  apt-get update
  apt-get install -y ca-certificates curl git python3
fi
systemctl enable --now docker
docker --host unix:///var/run/docker.sock version >/dev/null
compose_version=$(docker --host unix:///var/run/docker.sock compose version --short)
python3 - "$compose_version" <<'PY'
import re,sys
m=re.fullmatch(r'v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?',sys.argv[1])
if not m or tuple(map(int,m.groups())) < (2,20,0):
    raise SystemExit('Docker Compose 2.20 or newer is required; existing Docker was preserved.')
PY

release=$RELEASES/$REVISION
image=unibot-next:web-$REVISION
install -d -m 0755 "$RELEASES"
if [[ -e "$release" || -L "$release" ]]; then
  [[ -d "$release" && ! -L "$release" && -d "$release/.git" ]] || die 'Existing release path preserved; it is not a regular checkout.'
  [[ "$(git -C "$release" rev-parse HEAD)" == "$REVISION" && "$(git -C "$release" remote get-url origin)" == "$REPOSITORY" ]] || die 'Existing release revision/origin mismatch; nothing was overwritten.'
  [[ -z "$(git -C "$release" status --porcelain --untracked-files=normal)" ]] || die 'Existing release has local edits; preserve them and use a new release.'
else
  git clone --branch "$BRANCH" --single-branch "$REPOSITORY" "$release"
  git -C "$release" checkout --detach "$REVISION"
fi
helper=$release/tools/deploy/ubuntu-web.py
[[ -f "$helper" ]] || die 'This revision has no supported Ubuntu deployment helper.'
printf 'Selected immutable release: %s\n' "$REVISION"
install -d -m 0700 /etc/unibot-web /var/backups/unibot-web
new_config=0
data_done=0
stamp=$(date -u +%Y%m%dT%H%M%SZ)-$$
if [[ ! -e "$CONFIG" ]]; then
  python3 "$helper" init --domain "$DOMAIN" --image "$image"
  new_config=1
else
  python3 "$helper" validate --offline
  if ((WITH_BOT)); then
    python3 "$release/tools/deploy/install-data.py" --backup-directory "/var/backups/unibot-web/data-$stamp"
    data_done=1
  else
  python3 - "$CONFIG" "$image" <<'PY'
import sys
from pathlib import Path
values=dict(line.split('=',1) for line in Path(sys.argv[1]).read_text().splitlines() if line and not line.startswith('#'))
if values['APP_IMAGE']!=sys.argv[2]:
    raise SystemExit('Existing release config preserved. Follow the backup/upgrade guide to change releases.')
PY
  fi
fi
python3 "$helper" validate
if ((!data_done)); then python3 "$helper" build; fi
if ((REPLACE_PROXY)); then
  python3 "$release/tools/deploy/replace-web-proxy.py" --verify-origin "https://$DOMAIN" -- python3 "$helper" up
else
  python3 "$helper" up
fi
if ((BOOTSTRAP && new_config)); then
  python3 "$helper" bootstrap
else
  printf 'If no administrator exists, run: sudo python3 %s bootstrap\n' "$helper"
fi
python3 "$helper" check
if ((WITH_BOT)); then
  if ((!data_done)); then
    python3 "$release/tools/deploy/install-data.py" --backup-directory "/var/backups/unibot-web/data-$stamp"
  fi
  admin_args=("${ADMIN_IDS[@]}")
  if [[ -n "$OWNER_ID" ]]; then admin_args+=(--owner-id "$OWNER_ID"); fi
  if [[ -n "$EXPECTED_BOT" ]]; then admin_args+=(--expected-bot "$EXPECTED_BOT"); fi
  python3 "$release/tools/deploy/enable-telegram.py" prepare "${admin_args[@]}"
  python3 "$release/tools/deploy/enable-telegram.py" activate --backup-directory "/var/backups/unibot-web/telegram-$stamp"
  printf '\nTelegram and web installation ready: https://%s/login\nRelease: %s\n' "$DOMAIN" "$REVISION"
  exit 0
fi
printf '\nWeb-only installation ready: https://%s/login\nRelease: %s\n' "$DOMAIN" "$REVISION"
printf 'Telegram remains disabled. DNS/firewall and production-data migration are separate; previous releases and volumes were preserved.\n'
