#!/bin/sh
# socat SYSTEM child: stdin/stdout are the accepted TCP socket.
# Consume the request line so this is a real request/response exchange, then
# answer with a fixed body the client asserts on.
read -r _req
printf 'HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 18\r\nConnection: close\r\n\r\nvctl-dataplane-ok\n'
