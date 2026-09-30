#!/bin/sh
# socat SYSTEM child: a panel that ACCEPTS the controller. The daemon programs
# the firewall behind a commit-confirm deadman that only a successful check-in
# disarms; against a dead panel the ruleset reverts ~90 s later, in the middle
# of the UI run. One static answer serves both endpoints: RegisterResponse reads
# routerId/issuedToken/status, CheckInResponse reads status/jobs.
read -r req
len=0
while IFS= read -r h; do
	h=$(printf '%s' "$h" | tr -d '\r')
	[ -z "$h" ] && break
	case "$h" in [Cc]ontent-[Ll]ength:*) len=${h#*: } ;; esac
done
[ "$len" -gt 0 ] 2>/dev/null && head -c "$len" >/dev/null
printf '%s\n' "$(printf '%s' "$req" | tr -d '\r')" >> /tmp/ui-panel.log
body='{"routerId":"r-ui-stand","issuedToken":"tok-ui-stand","status":"approved","jobs":[]}'
printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s' "${#body}" "$body"
