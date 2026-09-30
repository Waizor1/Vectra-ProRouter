#!/bin/sh
# socat SYSTEM child for the stand's fake panel: stdin/stdout are the accepted
# TCP socket. Every request line is appended to $KS_PANEL_LOG, which is what the
# kill-switch mode reads to decide whether the router's control plane actually
# got out. The file lives on the shared mount namespace, so the inet-ns listener
# writing it and the router-ns assertions reading it see the same bytes.
#
# The reply is a 404 on purpose: the point is that the REQUEST ARRIVED, and a
# vctl agent probing this endpoint must get a clean protocol-level refusal
# rather than something it might mistake for a real panel.
read -r _req
printf '%s\n' "$_req" >> "${KS_PANEL_LOG:-/tmp/ks-panel-hits.log}"
printf 'HTTP/1.1 404 Not Found\r\nContent-Type: application/json\r\nContent-Length: 23\r\nConnection: close\r\n\r\n{"error":"stand-panel"}'
