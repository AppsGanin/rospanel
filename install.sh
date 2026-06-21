#!/usr/bin/env bash
#
# RosPanel quick installer.
#
#   curl -Ls https://raw.githubusercontent.com/AppsGanin/rospanel/main/install.sh | sudo bash
#
# Downloads the latest release binary, installs it as a systemd service via
# `rospanel install`, and prints the one-time first-run credentials. Xray,
# geo-bases and the TLS certificate are fetched by the panel itself on first run.
#
# Optional environment variables (all honoured by `rospanel install`):
#   ROSPANEL_HOST=vpn.example.com     bind a domain (enables ACME TLS)
#   ROSPANEL_ACME_EMAIL=you@mail.com  contact e-mail for Let's Encrypt
#   ROSPANEL_VERSION=v1.2.3           install a specific release (default: latest)
#
set -euo pipefail

REPO="AppsGanin/rospanel"
IMAGE="ghcr.io/appsganin/rospanel"   # GHCR image name is always lowercase
VERSION="${ROSPANEL_VERSION:-latest}"
ASSET=""   # resolved from the host architecture in the preflight checks below

# --- pretty output ----------------------------------------------------------
if [ -t 1 ]; then
	RED=$'\033[31m'; GRN=$'\033[32m'; YEL=$'\033[33m'; BLD=$'\033[1m'; RST=$'\033[0m'
else
	RED=''; GRN=''; YEL=''; BLD=''; RST=''
fi
info() { printf '%s==>%s %s\n' "$GRN" "$RST" "$*"; }
warn() { printf '%s==>%s %s\n' "$YEL" "$RST" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$RED" "$RST" "$*" >&2; exit 1; }

# --- preflight checks -------------------------------------------------------
[ "$(id -u)" -eq 0 ] || die "run as root (use: sudo bash <(curl -Ls ...))"
[ "$(uname -s)" = "Linux" ] || die "RosPanel runs on Linux + systemd only"
command -v systemctl >/dev/null 2>&1 || die "systemctl not found — systemd is required"

# Map the host arch to the release asset (must match the GOARCH names the
# release workflow builds: rospanel-linux-amd64 / rospanel-linux-arm64).
arch="$(uname -m)"
case "$arch" in
	x86_64|amd64)   ASSET="rospanel-linux-amd64" ;;
	aarch64|arm64)  ASSET="rospanel-linux-arm64" ;;
	*) die "unsupported architecture: ${arch}" ;;
esac

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$1" "$2"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$1" "$2"; }
else
	die "neither curl nor wget found — install one and retry"
fi

# --- ask for a domain (skipped if ROSPANEL_HOST is preset or no terminal) ---
# The recommended install is piped (curl … | sudo bash), so the script body
# already occupies stdin — read the prompts from the controlling terminal
# (/dev/tty) instead. Opening fd 3 on /dev/tty fails when there is no terminal
# (cron, CI, non-interactive SSH); in that case we skip and serve over IP.
if [ -z "${ROSPANEL_HOST:-}" ] && { exec 3</dev/tty; } 2>/dev/null; then
	printf '%sDomain for the panel%s (leave empty to serve over IP): ' "$BLD" "$RST" >&2
	read -r answer <&3 || answer=""
	ROSPANEL_HOST="$(printf '%s' "$answer" | tr -d '[:space:]')"
	if [ -n "$ROSPANEL_HOST" ] && [ -z "${ROSPANEL_ACME_EMAIL:-}" ]; then
		printf '%sACME e-mail%s for Let'\''s Encrypt (optional): ' "$BLD" "$RST" >&2
		read -r email <&3 || email=""
		ROSPANEL_ACME_EMAIL="$(printf '%s' "$email" | tr -d '[:space:]')"
	fi
	exec 3<&-
fi
export ROSPANEL_HOST ROSPANEL_ACME_EMAIL

# --- resolve download URL ---------------------------------------------------
if [ "$VERSION" = "latest" ]; then
	url="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
	url="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

# --- download ---------------------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
info "downloading ${BLD}${ASSET}${RST} (${VERSION})"
fetch "$tmp/rospanel" "$url" || die "download failed: $url"
[ -s "$tmp/rospanel" ] || die "downloaded file is empty — check the version tag"
chmod +x "$tmp/rospanel"

# --- install (copies binary → /usr/local/bin, writes unit, enables+starts) --
info "installing systemd service"
[ -n "${ROSPANEL_HOST:-}" ] && info "domain: ${BLD}${ROSPANEL_HOST}${RST} (ACME TLS will be requested)"
# Capture a journal cursor just before install so we read only NEW log lines.
# A cursor is monotonic by write order, so it is robust against clock skew —
# a wall-clock --since can wrongly exclude the FIRST-RUN line on hosts whose
# clock steps during boot. An empty cursor (no prior rospanel journal entries)
# safely falls back to an unscoped read.
log_cursor="$(journalctl -u rospanel --no-pager -n 0 --show-cursor 2>/dev/null | sed -n 's/^-- cursor: //p')"
"$tmp/rospanel" install

# --- first-run credentials --------------------------------------------------
# A fresh boot initialises the DB before it logs the FIRST-RUN block, which can
# take ~10-30s — so poll for up to ~40s instead of giving up after a few
# seconds, and wait for the whole block (the Password line) so a half-written
# journal entry is never printed.
info "fetching first-run credentials"
journal_args=(-u rospanel --no-pager)
if [ -n "$log_cursor" ]; then
	journal_args+=(--after-cursor="$log_cursor")
fi
creds=""
for ((i = 0; i < 40; i++)); do
	creds="$(journalctl "${journal_args[@]}" 2>/dev/null | grep -A6 FIRST-RUN || true)"
	case "$creds" in *Password*) break ;; esac
	sleep 1
done

echo
if [ -n "$creds" ]; then
	printf '%s\n' "$creds"
else
	warn "credentials not in the log yet — run:  journalctl -u rospanel | grep -A6 FIRST-RUN"
fi
echo
info "${GRN}done${RST} — open ${BLD}https://<your-domain-or-IP>/<secret>/${RST} and log in"
info "manage: ${BLD}rospanel status|restart|stop|uninstall${RST}"
