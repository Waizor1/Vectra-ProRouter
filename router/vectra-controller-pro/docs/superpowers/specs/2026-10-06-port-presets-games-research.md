# Port presets — games research (2026-10-06, official sources)

"both*" = the official source names the port but not the protocol; "both" is a superset.
Omitted: 7 Days to Die (no official protocol data).

```json
[
{"id":"minecraft-java","rules":[{"port":"25565","proto":"both"}],"sources":["https://help.minecraft.net/hc/en-us/articles/360058525452-How-to-Setup-a-Minecraft-Java-Edition-Server"],"note":"Mojang names port only; protocol unstated"},
{"id":"minecraft-bedrock","rules":[{"port":"19132","proto":"both"}],"sources":["https://learn.microsoft.com/en-us/minecraft/creator/documents/bedrockserver/getting-started"],"note":"19133 is server-portv6 (IPv6 only); protocol unstated"},
{"id":"cs2-srcds","rules":[{"port":"27015","proto":"both"}],"sources":["https://developer.valvesoftware.com/wiki/Source_Dedicated_Server"],"note":"27020/udp SourceTV optional"},
{"id":"rust","rules":[{"port":"28015","proto":"udp"},{"port":"28017","proto":"udp"}],"sources":["https://wiki.facepunch.com/rust/Creating-a-server"],"note":"28017 = query; RCON 28016/tcp optional"},
{"id":"valheim","rules":[{"port":"2456-2457","proto":"both"}],"sources":["https://valheim.com/support/a-guide-to-dedicated-servers/"],"note":"-crossplay needs no forwarding; protocol unstated"},
{"id":"terraria","rules":[{"port":"7777","proto":"tcp"}],"sources":["https://terraria.wiki.gg/wiki/Server"],"note":""},
{"id":"ark-survival-evolved","rules":[{"port":"7777-7778","proto":"udp"},{"port":"27015","proto":"udp"}],"sources":["https://ark.wiki.gg/wiki/Dedicated_server_setup"],"note":"ASE; ASA not verified"},
{"id":"palworld","rules":[{"port":"8211","proto":"udp"}],"sources":["https://docs.palworldgame.com/getting-started/requirements"],"note":""},
{"id":"factorio","rules":[{"port":"34197","proto":"udp"}],"sources":["https://wiki.factorio.com/Multiplayer"],"note":""},
{"id":"satisfactory","rules":[{"port":"7777","proto":"both"},{"port":"8888","proto":"tcp"}],"sources":["https://satisfactory.wiki.gg/wiki/Dedicated_servers"],"note":"1.0+; 15000/15777 retired"},
{"id":"project-zomboid","rules":[{"port":"16261-16262","proto":"udp"}],"sources":["https://pzwiki.net/wiki/Dedicated_server"],"note":""},
{"id":"enshrouded","rules":[{"port":"15637","proto":"both"}],"sources":["https://enshrouded.zendesk.com/hc/en-us/articles/16056312924957-Dedicated-Server-FAQ"],"note":"protocol unstated"},
{"id":"dont-starve-together","rules":[{"port":"10999","proto":"udp"}],"sources":["https://forums.kleientertainment.com/forums/topic/64552-dedicated-server-settings-guide/"],"note":"each extra shard needs its own port"}
]
```
Research complete — see below.

## Consoles / remote / media / home / voice / VPN (official sources, 2026-10-06)
Omitted: Nintendo Switch (Nintendo withdrew its port article), Parsec (ports user-set; page unreadable).
Caveats: PlayStation — Sony doesn't mark inbound; dropped TCP 80/443 and UDP 49152-65535. Xbox — 9002/udp optional. Steam — local Remote Play ports. Home Assistant — HAOS default 80 since 2026.8, Container 8123. WireGuard — 51820 is conventional (ListenPort random if unset). Jellyfin/Emby 8920 only with HTTPS.

```json
[
{"id":"playstation","rules":[{"port":"3478-3479","proto":"both"},{"port":"3480","proto":"tcp"}],"sources":["https://www.playstation.com/en-gb/support/error-codes/ps5/nw-102417-5/"]},
{"id":"xbox","rules":[{"port":"3074","proto":"both"},{"port":"9002","proto":"udp"}],"sources":["https://support.xbox.com/en-US/help/hardware-network/connect-network/network-ports-used-xbox-live"]},
{"id":"steam-remote-play","rules":[{"port":"27031-27036","proto":"udp"},{"port":"27036","proto":"tcp"}],"sources":["https://help.steampowered.com/en/faqs/view/2EA8-4D75-DA21-31EB"]},
{"id":"sunshine","rules":[{"port":"47984","proto":"tcp"},{"port":"47989","proto":"tcp"},{"port":"48010","proto":"tcp"},{"port":"47998-48000","proto":"udp"}],"sources":["https://docs.lizardbyte.dev/projects/sunshine/latest/md_docs_2configuration.html"]},
{"id":"rdp","rules":[{"port":"3389","proto":"both"}],"sources":["https://learn.microsoft.com/en-us/windows-server/remote/remote-desktop-services/clients/change-listening-port"]},
{"id":"vnc","rules":[{"port":"5900","proto":"tcp"}],"sources":["https://www.rfc-editor.org/rfc/rfc6143"]},
{"id":"ssh","rules":[{"port":"22","proto":"tcp"}],"sources":["https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.xhtml"]},
{"id":"anydesk","rules":[{"port":"7070","proto":"tcp"}],"sources":["https://support.anydesk.com/docs/settings"]},
{"id":"plex","rules":[{"port":"32400","proto":"tcp"}],"sources":["https://support.plex.tv/articles/200931138-troubleshooting-remote-access/"]},
{"id":"jellyfin","rules":[{"port":"8096","proto":"tcp"},{"port":"8920","proto":"tcp"}],"sources":["https://jellyfin.org/docs/general/post-install/networking/"]},
{"id":"emby","rules":[{"port":"8096","proto":"tcp"},{"port":"8920","proto":"tcp"}],"sources":["https://emby.media/support/articles/Connectivity.html"]},
{"id":"transmission","rules":[{"port":"51413","proto":"both"}],"sources":["https://github.com/transmission/transmission/blob/main/docs/Port-Forwarding-Guide.md"]},
{"id":"synology-dsm","rules":[{"port":"5000","proto":"tcp"},{"port":"5001","proto":"tcp"}],"sources":["https://kb.synology.com/en-global/DSM/tutorial/What_network_ports_are_used_by_Synology_services"]},
{"id":"web-server","rules":[{"port":"80","proto":"tcp"},{"port":"443","proto":"tcp"}],"sources":["https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.xhtml"]},
{"id":"nextcloud","rules":[{"port":"443","proto":"tcp"}],"sources":["https://github.com/nextcloud/all-in-one/blob/main/readme.md"]},
{"id":"home-assistant","rules":[{"port":"8123","proto":"tcp"}],"sources":["https://www.home-assistant.io/integrations/http/"]},
{"id":"hikvision","rules":[{"port":"80","proto":"tcp"},{"port":"8000","proto":"tcp"},{"port":"554","proto":"tcp"}],"sources":["https://supportusa.hikvision.com/support/solutions/articles/17000128725"]},
{"id":"dahua","rules":[{"port":"80","proto":"tcp"},{"port":"37777","proto":"tcp"}],"sources":["https://www.dahuasecurity.com/about-dahua/news-events/notice/how-to-setup-remote-access-for-nvr"]},
{"id":"teamspeak3","rules":[{"port":"9987","proto":"udp"},{"port":"30033","proto":"tcp"}],"sources":["https://github.com/TeamSpeak-Systems/teamspeak-linux-docker-images/blob/master/alpine/Dockerfile"]},
{"id":"mumble","rules":[{"port":"64738","proto":"both"}],"sources":["https://github.com/mumble-voip/mumble/blob/master/auxiliary_files/mumble-server.ini"]},
{"id":"wireguard","rules":[{"port":"51820","proto":"udp"}],"sources":["https://www.wireguard.com/quickstart/"]},
{"id":"openvpn","rules":[{"port":"1194","proto":"udp"}],"sources":["https://openvpn.net/community-resources/reference-manual-for-openvpn-2-6/"]}
]
```
