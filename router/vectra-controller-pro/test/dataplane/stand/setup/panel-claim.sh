#!/bin/sh
# socat SYSTEM child: a panel that accepts the controller and, as the real one
# may, answers with Vectra's claim key and bot (ADR-0006) — the key is the one
# of ui/contract/claim-vector.json. Two files steer what it says next:
#   /tmp/setup-panel-owner     the owner it names (one line, plain ASCII);
#   /tmp/setup-panel-released  the owner unbound the router in the Vectra app:
#                              "released": true, "owner": null (wins).
# It logs every request line WITH its body, so the stand can read the claim
# each check-in carries.
read -r req
len=0
while IFS= read -r h; do
	h=$(printf '%s' "$h" | tr -d '\r')
	[ -z "$h" ] && break
	case "$h" in [Cc]ontent-[Ll]ength:*) len=${h#*: } ;; esac
done
body=''
[ "$len" -gt 0 ] 2>/dev/null && body=$(head -c "$len")
printf '%s %s\n' "$(printf '%s' "$req" | tr -d '\r')" "$body" >> /tmp/setup-panel.log
owner=''
if [ -e /tmp/setup-panel-released ]; then
	owner=',"released":true,"owner":null'
elif [ -s /tmp/setup-panel-owner ]; then
	owner=",\"owner\":{\"label\":\"$(head -n 1 /tmp/setup-panel-owner)\"}"
fi
resp="{\"routerId\":\"r-setup-stand\",\"issuedToken\":\"tok-setup-stand\",\"status\":\"approved\",\"jobs\":[],\"claimKey\":{\"kid\":1,\"publicKey\":\"dsTt0kkqBdr7emCqoixBS+iz/r9Y86jyAxEzzKARNBs=\"},\"botUsername\":\"VectraStandBot\"$owner}"
printf 'HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s' "${#resp}" "$resp"
