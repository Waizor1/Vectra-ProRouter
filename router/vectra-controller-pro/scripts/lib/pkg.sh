# shellcheck shell=bash
# The vectra-controller-pro package as openwrt/Makefile defines it, for the
# builds that do not go through the OpenWrt SDK (build-local-ipk.sh,
# build-pro-feed.sh). Everything is derived from the Makefile, never kept in
# parallel:
#   - the payload: every `$(INSTALL_BIN|DATA|CONF) ./files/<src> $(1)/<dst>` line
#     replayed with the same mode (0755 / 0644 / 0600);
#   - postinst, prerm and postrm: extracted from the Makefile's defines;
#   - Depends: the Makefile's DEPENDS;
#   - the version: VECTRA_VERSION-rVECTRA_RELEASE.
# Source it; it needs PKG_MODULE (the router/vectra-controller-pro directory).

PKG_MAKEFILE="$PKG_MODULE/openwrt/Makefile"

pkg_version() { # -> 0.5.0-r1
	local ver rel
	ver="$(sed -n 's/^VECTRA_VERSION?=\(.*\)$/\1/p' "$PKG_MAKEFILE")"
	rel="$(sed -n 's/^VECTRA_RELEASE?=\(.*\)$/\1/p' "$PKG_MAKEFILE")"
	[[ -n "$ver" && -n "$rel" ]] || { echo "cannot read the version from $PKG_MAKEFILE" >&2; return 1; }
	echo "$ver-r$rel"
}

pkg_depends() { # -> "ca-bundle, jsonfilter, ..."
	sed -n 's/^[[:space:]]*DEPENDS:=//p' "$PKG_MAKEFILE" | tr ' ' '\n' | sed -n 's/^+//p' | paste -sd, - | sed 's/,/, /g'
}

pkg_stage_payload() { # <data-dir>: every file the package installs, at its path and mode (vctl itself excluded)
	local data="$1" kind src dst mode n=0
	while read -r kind src dst; do
		case "$kind" in
		'$(INSTALL_BIN)') mode=0755 ;;
		'$(INSTALL_DATA)') mode=0644 ;;
		'$(INSTALL_CONF)') mode=0600 ;;
		*) continue ;;
		esac
		src="${src#./files/}"
		dst="${dst#\$(1)}"
		install -d "$data$(dirname "$dst")"
		install -m "$mode" "$PKG_MODULE/openwrt/files/$src" "$data$dst"
		n=$((n + 1))
	done < <(grep -E '^\s*\$\(INSTALL_(BIN|DATA|CONF)\) \./files/' "$PKG_MAKEFILE" | awk '{print $1, $2, $3}')
	[[ $n -gt 0 ]] || { echo "no install lines found in $PKG_MAKEFILE" >&2; return 1; }
	echo "$n"
}

pkg_maintainer_scripts() { # <control-dir>: postinst, prerm, postrm as the Makefile defines them
	local ctrl="$1" s
	for s in postinst prerm postrm; do
		awk -v want="define Package/\$(PKG_NAME)/$s" '
			$0 == want { inblk = 1; next }
			inblk && $0 == "endef" { exit }
			inblk { print }
		' "$PKG_MAKEFILE" | sed 's/\$\$/$/g' > "$ctrl/$s"
		if [[ -s "$ctrl/$s" ]]; then
			grep -q '^#!/bin/sh' "$ctrl/$s" || { echo "$s in $PKG_MAKEFILE does not start with #!/bin/sh" >&2; return 1; }
			chmod 0755 "$ctrl/$s"
		else
			rm -f "$ctrl/$s"
		fi
	done
}

pkg_commit_epoch() { # the last commit's time: a build stamp that is the same on every rebuild
	git -C "$PKG_MODULE" log -1 --format=%ct 2>/dev/null || echo 0
}

pkg_build_vctl() { # <out-binary> <GOARCH> [GOARM] [GOMIPS]: a static vctl, stamped with the package version
	local out="$1" goarch="$2" goarm="${3:-}" gomips="${4:-}" full commit dirty="" built
	full="$(pkg_version)"
	commit="$(git -C "$PKG_MODULE" rev-parse --short HEAD 2>/dev/null || echo dev)"
	git -C "$PKG_MODULE" diff --quiet HEAD -- . 2>/dev/null || dirty="-dirty"
	built="$(TZ=UTC git -C "$PKG_MODULE" log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo dev)"
	case "${VECTRA_BUILD_PROFILE:-plain}" in
	plain) ;;
	scoped-v1)
		. "$PKG_MODULE/scripts/lib/scoped-profile.sh"
		pkg_build_scoped_vctl "$out" "$goarch" "$goarm" "$gomips" "$full" "$commit$dirty" "$built" || return
		chmod 0755 "$out"
		return ;;
	*) echo 'unknown VECTRA_BUILD_PROFILE' >&2; return 1 ;;
	esac
	( cd "$PKG_MODULE" && GOOS=linux GOARCH="$goarch" GOARM="$goarm" GOMIPS="$gomips" GOMIPS64="$gomips" CGO_ENABLED=0 \
		go build -trimpath -buildvcs=false \
		-ldflags "-s -w -buildid= -X main.Version=$full -X main.runtimeVersion=$full -X main.Commit=$commit$dirty -X main.BuildDate=$built" \
		-o "$out" ./cmd/vctl )
	chmod 0755 "$out"
}
