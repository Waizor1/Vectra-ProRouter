#!/bin/sh
# The "PassWall stack" process. Sleeps forever, opens no sockets, touches no
# rules. Its only job is to be a process the stand can observe, so that "the
# other proxy stack is running" is a measurement.
while :; do
	sleep 3600
done
