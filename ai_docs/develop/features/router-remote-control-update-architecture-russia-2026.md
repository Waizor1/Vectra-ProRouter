# Архитектура удаленного управления и обновлений роутеров в России на 2026-04

Дата подготовки: 2026-04-04
Назначение: подобрать практические способы связи роутер ↔ управляющий сервер и способы доставки обновлений для будущего OpenWrt-приложения с учетом российских блокировок на апрель 2026 года.

Связанный контекст:

- [Россия 2026-04: блокировки и каналы связи роутер ↔ управляющий сервер](russia-router-control-plane-blocking-kb.md)
- [OpenWrt 24.xx: база знаний по созданию приложений](openwrt24-app-development-knowledge-base/README.md)
- [Release, feed, подписи и обновления](openwrt24-app-development-knowledge-base/05-release-feed-signing-and-updates.md)

## 1. Короткий ответ

Если проектировать систему с нуля под российскую реальность апреля 2026 года, то базовая архитектура должна быть такой:

- роутер сам инициирует исходящее соединение;
- основной transport для управления: обычный `HTTPS` на `TCP/443`;
- ускоряющий transport для near-real-time: `WSS` на `TCP/443`, но только как дополнительный режим;
- guaranteed fallback: короткий `HTTPS` polling или `HTTPS` long-polling;
- обновления приложения: собственный подписанный `opkg` feed по `HTTPS`;
- обновления прошивки: отдельный signed image workflow по `HTTPS`, не смешанный с app update;
- router-facing endpoints должны быть как минимум в двух независимых точках доступности, а лучше в разных ASN/провайдерах;
- операторский интерфейс и router-facing ingress лучше разделять.

Самый важный вывод:

- будущий продукт не должен зависеть ни от VPN overlay, ни от Telegram/WhatsApp, ни от одного иностранного VPS/CDN, ни от `QUIC`-only транспорта.

## 2. Что рекомендуется использовать

### 2.1 Recommended baseline

| Канал / механизм | Для чего | Статус | Почему |
|------------------|-----------|--------|--------|
| `HTTPS` short polling на `TCP/443` | heartbeat, desired state, команды, ack, telemetry | рекомендовано как базовая линия | выглядит как обычный web/API трафик, не требует отдельного VPN-протокола, хорошо ложится на pull-модель |
| `HTTPS` long polling на `TCP/443` | более быстрые команды без постоянного websocket | рекомендовано | дает меньше latency, чем периодический polling, но остается в рамках обычного HTTP |
| `WSS` на `TCP/443` | near-real-time control, stream команд, live events | рекомендовано как дополнительный канал, но не как единственный | практично для интерактивности, но должен иметь автоматический fallback на обычный HTTP |
| собственный `opkg` feed по `HTTPS` | обновления приложения и LuCI-пакета | рекомендовано | это штатный OpenWrt lifecycle для `24.xx`: `opkg`, `Packages.gz`, `Packages.sig`, `usign`, `opkg-key` |
| signed manifest over `HTTPS` | выбор версии, канал обновлений, rollout policy | рекомендовано | дает control plane над тем, когда и что именно должен установить роутер |
| router-initiated firmware download over `HTTPS` | обновления прошивки | рекомендовано как отдельный workflow | firmware lifecycle нужно отделять от app update и проверять через `sysupgrade -T` или `validate_firmware_image` |

### 2.2 Conditionally acceptable

| Канал / механизм | Для чего | Статус | Комментарий |
|------------------|-----------|--------|-------------|
| `MQTT over WSS` на `443` | pub/sub модель, события, fan-out | допустимо, если действительно нужен broker pattern | использовать только поверх `WSS/443`; raw `1883/8883` как внешний основной канал не закладывать |
| `gRPC` over `HTTP/2` на `443` | typed RPC между агентом и сервером | допустимо | подходит, если агент не слишком тяжелый для OpenWrt-роутеров; fallback на обычный `HTTPS` все равно нужен |
| `SSE` + обычный `HTTPS POST` | односторонние server-to-device события | допустимо, но обычно хуже `WSS` | годится для простых случаев, но двусторонняя интерактивность неудобнее |
| manual package fallback через download URL + `opkg install /tmp/*.ipk` | break-glass обновление приложения | допустимо как запасной путь | не должен быть главным update lifecycle, но полезен при проблемах с индексом feed |

## 3. Что не стоит закладывать в продукт

| Подход | Почему не подходит как основа |
|--------|-------------------------------|
| `OpenVPN`, `WireGuard`, `IKEv2/IPsec`, `L2TP`, `SOCKS5`, `VLESS` как канал управления | эти transport-ы уже нельзя считать гарантированно доступными в России |
| `HTTP/3` / `QUIC` как единственный transport | `QUIC` уже попадает в зону прямого DPI-риска и collateral блокировок |
| `Telegram` или `WhatsApp` как OOB-канал управления | к апрелю 2026 года это уже ненадежная база для critical operations |
| direct inbound admin port на роутере из интернета | NAT, exposure, сканирование, зависимость от входящего reachability, лишняя поверхность атаки |
| reverse SSH tunnel как штатный control plane | operationally fragile, плохо масштабируется, слишком похоже на частный туннельный transport |
| один server endpoint на одном VPS/CDN | слишком высокий риск региональной или ASN-level недоступности |
| push-only модель без device pull | если server-to-router путь рвется, устройство перестает управляться |

## 4. Рекомендуемая архитектура продукта

### 4.1 Разделить plane-ы

Для приложения лучше сразу проектировать не один «сервер для всего», а три слоя:

1. `Operator plane`
   Веб-интерфейс, API для операторов, аудит, multi-tenant logic, rollout policy.

2. `Router-facing control plane`
   Edge API, с которым общаются сами роутеры по `HTTPS` или `WSS` на `443`.

3. `Artifact/update plane`
   Репозиторий пакетов, manifest service, feed mirrors, firmware image storage.

Почему это важно:

- router-facing traffic должен оставаться максимально «обычным» и независимым от внутренних операторских компонентов;
- package feed и firmware storage лучше масштабируются отдельно;
- можно менять backend или админку, не меняя протокол устройства.

### 4.2 Control loop для роутера

Базовая логика агента на роутере:

1. При старте агент читает локальный bootstrap config:
   - device id
   - device key или client certificate
   - список control endpoints
   - список update endpoints
   - rollout channel: `stable`, `beta`, `canary`

2. Агент делает `POST /v1/check-in` по `HTTPS`.

3. Сервер отвечает:
   - текущей политикой
   - `desired_revision`
   - интервалом следующего check-in
   - флагом, нужен ли upgrade
   - если требуется, адресом long-poll или `WSS`

4. Если websocket недоступен, агент остается на `HTTPS` polling/long-polling и продолжает работать.

5. Все команды должны быть:
   - idempotent
   - versioned
   - с bounded execution time
   - с отчетом о результате после выполнения

Практический смысл:

- сервер не должен зависеть от возможности «достучаться до роутера снаружи»;
- устройство всегда остается управляемым, пока у него есть обычный исходящий web-доступ.

### 4.3 Control transport: рекомендуемый набор

Лучший practical stack для первой версии:

- primary: `HTTPS` polling;
- secondary: `HTTPS` long-polling;
- optional accelerator: `WSS` на `443`.

Хороший стартовый режим:

- heartbeat каждые `60-180` секунд с jitter;
- long-poll `20-60` секунд для роутеров, которым нужна более быстрая реакция;
- `WSS` пытаться поднять только после успешного базового check-in;
- если `WSS` падает, не считать устройство offline, а молча возвращаться на long-poll.

Это важнее, чем «самый модный transport», потому что:

- проблема в России сейчас не только в latency;
- проблема в том, что control plane должен переживать деградацию transport-а без ручного recovery.

## 5. Как именно доставлять обновления

### 5.1 Обновления приложения

Для OpenWrt `24.xx` обновление вашего приложения должно идти через штатный package lifecycle:

- отдельный core package, например `myapp-agent`;
- отдельный LuCI package, например `luci-app-myapp`;
- свой `opkg` feed;
- `Packages`, `Packages.gz`, `Packages.sig`;
- доверенный public key через `opkg-key`.

Практический flow:

1. Роутер получает по control API информацию, что доступна новая версия.
2. Агент делает `opkg update`.
3. Агент проверяет, доступен ли target version в нужном канале.
4. Агент выполняет `opkg upgrade myapp-agent` и при необходимости `opkg upgrade luci-app-myapp`.
5. Агент отправляет статус upgrade обратно на сервер.

Почему это лучший default:

- это нативно для OpenWrt;
- signature chain уже понятна;
- легче поддерживать install/upgrade/remove lifecycle;
- можно использовать зеркала, не теряя доверие, пока сохраняется подпись индекса.

### 5.2 Обновления прошивки

Firmware update не стоит смешивать с app update.

Для прошивки лучше делать отдельный workflow:

1. Сервер публикует firmware manifest:
   - board/target compatibility
   - версия
   - sha256
   - URL основного и запасного источника
   - rollout policy

2. Роутер скачивает образ по `HTTPS`.

3. Перед применением выполняется проверка:
   - `sysupgrade -T /tmp/firmware.bin`
   - или `ubus call system validate_firmware_image '{ "path": "/tmp/firmware.bin" }'`

4. Только после успешной валидации и policy check агент применяет обновление.

Итог:

- приложение и прошивка должны жить в разных release lanes;
- критичную логику доставки прошивки нельзя строить на тех же допущениях, что и обычный app patch.

## 6. Как обеспечить устойчивость в России

### 6.1 Endpoint strategy

В bootstrap config устройства должны быть заранее известны:

- минимум `2` control endpoint-а;
- минимум `2` update mirror endpoint-а;
- они не должны сидеть на одном IP range и одном ASN;
- желательно, чтобы хотя бы одна router-facing точка была не в той же облачной корзине, что и остальные.

Полезные правила:

- не полагаться на один CDN;
- не полагаться на один hostname;
- не полагаться только на DNS как единственный механизм переключения;
- иметь и hostname list, и запасные endpoint-ы в конфиге агента.

### 6.2 Retry strategy

На устройстве нужны:

- exponential backoff;
- jitter;
- store-and-forward очередь для telemetry и job result;
- локальный last-known desired state;
- ограничение на размер очереди и TTL задач.

Без этого любой кратковременный провал в доступности control endpoint-а будет выглядеть как «потеряли роутер».

### 6.3 Split geography

Если операторы продукта работают вне России, разумнее закладывать split architecture:

- routers общаются с router-facing ingress, который оптимизирован под доступность из российских сетей;
- operator plane и основная business logic могут оставаться в другой географии;
- артефакты обновлений можно зеркалировать независимо от control API.

Это не про обход блокировок.
Это про то, чтобы router-facing часть не была привязана к единственной иностранной точке отказа.

## 7. Рекомендуемый продуктовый MVP

Для первой версии приложения достаточно следующего:

1. Агента с `HTTPS` polling на `443`.
2. JSON API с desired-state моделью.
3. Команд вида:
   - `apply_config`
   - `restart_service`
   - `run_diagnostics`
   - `report_inventory`
   - `begin_update`
4. Собственного подписанного `opkg` feed.
5. Зеркала для `Packages.gz` и `.ipk`.
6. Отдельного firmware manifest API, даже если firmware update появится позже.
7. Необязательного `WSS` режима как ускорителя, но не как зависимости.

Этот MVP уже покрывает:

- удаленный контроль;
- обновления приложения;
- диагностический обмен;
- rollout по каналам;
- работу в деградирующих сетевых условиях.

## 8. Что я бы не делал в V1

- не строил бы V1 на `MQTT` broker-first архитектуре, если задача сводится к device check-in и job dispatch;
- не делал бы в V1 обязательный `WSS` для всех устройств;
- не закладывал бы device reachability через входящие соединения на роутер;
- не смешивал бы firmware delivery и app delivery в один пакетный механизм;
- не делал бы «аварийный доступ» через `Telegram` бот;
- не ставил бы единственный foreign-only origin для package feed.

## 9. Практическая рекомендация именно для вашего приложения

Если задача звучит как «мы хотим управлять парком роутеров и безопасно раскатывать обновления нашего приложения», то лучший practical choice сейчас такой:

- transport:
  `HTTPS` polling + optional `WSS` fallback-aware channel;
- control model:
  desired-state, а не interactive shell;
- app update model:
  signed `opkg` feed;
- firmware update model:
  отдельный signed manifest + `sysupgrade` validation path;
- availability model:
  multi-endpoint, multi-mirror, multi-provider;
- security model:
  per-device credentials, подпись артефактов, отсутствие inbound admin exposure.

Если упростить до одного предложения:

- делайте устройство как обычный outbound web client, а обновления как обычный signed OpenWrt package lifecycle.

## 10. Источники и степень уверенности

### Подтверждено локальными OpenWrt KB в этом workspace

- [OpenWrt 24.xx App Development Cheatsheet](openwrt24-app-development-knowledge-base/06-cheatsheet.md)
- [OpenWrt 24.xx: база знаний по созданию приложений](openwrt24-app-development-knowledge-base/README.md)
- [Release, feed, подписи и обновления](openwrt24-app-development-knowledge-base/05-release-feed-signing-and-updates.md)
- [OpenWrt 24.xx CLI Cheatsheet](openwrt24-console-knowledge-base/06-cheatsheet.md)

### Подтверждено внешними источниками

- OpenWrt Feeds: [openwrt.org/docs/guide-developer/feeds](https://openwrt.org/docs/guide-developer/feeds)
- OpenWrt package signatures: [openwrt.org/docs/guide-user/security/signatures](https://openwrt.org/docs/guide-user/security/signatures)
- Blocking context:
  [russia-router-control-plane-blocking-kb.md](russia-router-control-plane-blocking-kb.md)

### Явно инженерные выводы

Следующие пункты являются не «официальной спецификацией», а архитектурной рекомендацией:

- использовать `HTTPS` polling как primary transport;
- считать `WSS` ускорителем, а не обязательным transport-ом;
- делать split между operator plane, router-facing control plane и update plane;
- закладывать multi-endpoint и multi-mirror с первого релиза.
