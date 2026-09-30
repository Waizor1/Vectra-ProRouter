# Real OpenWrt 24.10 userland (same branch as the fleet's 24.10.6), aarch64.
# Docker prints a platform-mismatch warning because this image declares
# `linux/aarch64_generic`; it is harmless — do not "fix" it with --platform.
FROM openwrt/rootfs:aarch64_generic-24.10.8

# opkg needs /var/lock to exist in a container rootfs.
# jshn/jsonfilter are what render-xray-config.sh and the init script use; procd
# and ubus are needed because the procd mode really does run a service manager.
# rpcd, uhttpd and luci-base are the router UI's path (MODE=ui): the package's
# rpcd plugin and ACL, served to a real LuCI over a real /ubus endpoint.
RUN mkdir -p /var/lock /var/run /usr/share/v2ray \
 && opkg update \
 && opkg install ip-full nftables curl socat openssl-util ca-bundle \
      jshn jsonfilter procd ubus \
      rpcd uhttpd uhttpd-mod-ubus luci-base luci-theme-bootstrap \
 && rm -rf /var/opkg-lists

# Real package payload: the same files the .ipk installs, at the same paths.
# (The .ipk itself is built for aarch64_cortex-a53 by the OpenWrt SDK and
# predates the fixes under test, so the freshly built vctl is dropped in
# instead — see README.)
COPY build/pkg/etc/init.d/vectra-controller-pro                          /etc/init.d/vectra-controller-pro
COPY build/pkg/etc/config/vectra-controller-pro                          /etc/config/vectra-controller-pro
COPY build/pkg/etc/uci-defaults/90_vectra_controller_pro_defaults        /etc/uci-defaults/90_vectra_controller_pro_defaults
COPY build/pkg/usr/libexec/vectra-controller-pro/render-xray-config.sh   /usr/libexec/vectra-controller-pro/render-xray-config.sh
COPY build/pkg/usr/sbin/vctl-xray-wrapper                                /usr/sbin/vctl-xray-wrapper

COPY build/vctl        /usr/sbin/vctl
COPY build/xray        /usr/bin/xray
# The reporter (ADR-0007) and the reports mode's stand-ins: a stub Connect
# that verifies what it gets, and Vectra's test key for the router's state.
COPY build/vectra-reporter /usr/sbin/vectra-reporter
COPY build/reportsink build/standkey /stand/bin/
COPY build/reporter-etc/ /etc/
# vctl's own geo directory (vectra-geodata's on a router), which it reads —
# a config's /usr/share/v2ray included. The same files stay in
# /usr/share/v2ray for what else a mode runs (PassWall's own xray).
COPY build/geoip.dat   /usr/share/vectra-controller-pro/geo/geoip.dat
COPY build/geosite.dat /usr/share/vectra-controller-pro/geo/geosite.dat
COPY build/geoip.dat   /usr/share/v2ray/geoip.dat
COPY build/geosite.dat /usr/share/v2ray/geosite.dat

# The same payload again, this time as an unpacked DATA TREE the procd mode
# assembles into a real .ipk (run.sh drops the freshly built vctl into it, and
# writes pkg-control/ from the maintainer scripts it extracts from the Makefile).
COPY build/pkg         /stand/pkg
COPY build/pkg-control /stand/pkg-control

COPY stand/ /stand/

RUN chmod +x /usr/sbin/vctl /usr/bin/xray /usr/sbin/vctl-xray-wrapper \
      /etc/init.d/vectra-controller-pro /etc/uci-defaults/90_vectra_controller_pro_defaults \
      /usr/libexec/vectra-controller-pro/render-xray-config.sh \
      /stand/stand.sh /stand/http-reply.sh /stand/panel-reply.sh /stand/build-ipk.sh \
      /stand/legacy-stub/vectra-controller /stand/legacy-stub/legacy-agent-loop.sh \
      /stand/legacy-stub/passwall2 /stand/legacy-stub/passwall-loop.sh \
      /stand/ui/panel-ok.sh \
      /stand/pkg/etc/init.d/vectra-controller-pro \
      /stand/pkg/etc/uci-defaults/90_vectra_controller_pro_defaults \
      /stand/pkg/usr/libexec/vectra-controller-pro/render-xray-config.sh \
      /stand/pkg/usr/sbin/vctl-xray-wrapper /stand/pkg/usr/sbin/vctl \
      /stand/pkg-control/postinst /stand/pkg-control/prerm /stand/pkg-control/postrm \
 && openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
      -keyout /stand/tls/key.pem -out /stand/tls/cert.pem \
      -subj /CN=origin.stand -addext subjectAltName=IP:44.44.44.44 2>/dev/null \
 && /etc/uci-defaults/90_vectra_controller_pro_defaults >/dev/null 2>&1 || true

ENTRYPOINT ["/stand/stand.sh"]
