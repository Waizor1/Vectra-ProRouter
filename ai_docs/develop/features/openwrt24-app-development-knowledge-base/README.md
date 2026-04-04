# OpenWrt 24.xx: база знаний по созданию приложений

Дата подготовки: 2026-04-04

Эта папка фиксирует полный lifecycle разработки приложений и пакетов для OpenWrt `24.xx` с практической базовой линией `24.10.x`. Речь идет не только о написании кода, а о всей цепочке: выбрать правильную build-среду, оформить пакет, подключить UCI и `procd`, при необходимости сделать LuCI-фронтенд, собрать `.ipk`, развернуть на роутере, опубликовать свой feed и потом безопасно обновлять приложение дальше.

Для runtime-администрирования уже установленного роутера использовать соседнюю базу:

- [OpenWrt console KB](../openwrt24-console-knowledge-base/README.md)

Эта новая база отвечает именно на вопрос "как делать свои OpenWrt-приложения так, чтобы их можно было сопровождать и обновлять".

## Что считается приложением в OpenWrt

В контексте OpenWrt приложение обычно поставляется как пакет `.ipk` и может включать один или несколько слоев:

- daemon или CLI binary
- shell-скрипты
- файл конфигурации в `/etc/config/*`
- init-скрипт `/etc/init.d/*`
- one-shot migration/default scripts в `/etc/uci-defaults/*`
- LuCI-фронтенд как отдельный `luci-app-*` пакет
- зависимости на системные библиотеки и другие пакеты

## Что покрывает эта база

- Когда брать SDK, а когда полный buildroot
- Как подключать локальный feed и собирать свой пакет
- Как устроен `Makefile` пакета OpenWrt
- Как делать service lifecycle через `rc.common` и `procd`
- Как работать с UCI, `uci-defaults`, `conffiles`, `postinst`
- Как делать отдельный LuCI-пакет под `24.10`
- Как тестировать install/remove/upgrade
- Как безопасно прогонять ранние live-тесты на реальном роутере без package install и service registration
- Как публиковать собственный `opkg` feed и подписывать его
- Как организовать последующие обновления и миграции конфигурации

## Что сознательно не покрывается

- Разработка kernel modules
- Сборка собственного OpenWrt firmware image как основная тема
- Все возможные build-системы мира во всех нюансах
- Детальная разработка драйверов и BSP

## Главные выводы

- Для OpenWrt `24.xx` пакетный путь по умолчанию — `opkg` + `.ipk`, не `apk`
- Для приложений лучший рабочий цикл обычно такой:
  - разработка исходников отдельно
  - packaging в локальном feed
  - сборка через matching SDK или buildroot
  - публикация своего пакета в подписанный feed
  - обновление через `opkg update` и `opkg upgrade <pkg>`
- Если приложение содержит сервис, нужно сразу проектировать не только `start`, но и upgrade/migration/reload lifecycle
- Если есть web UI, лучше разделять core package и `luci-app-*`

## Как читать эту папку

1. Начать с [06-cheatsheet.md](06-cheatsheet.md), если нужен быстрый operational path
2. Прочитать [01-lifecycle-and-toolchains.md](01-lifecycle-and-toolchains.md), чтобы выбрать правильную среду и понять полный lifecycle
3. Перейти к [02-package-layout-and-makefile.md](02-package-layout-and-makefile.md) для anatomy пакета
4. Затем разобрать [03-service-config-and-luci.md](03-service-config-and-luci.md) для UCI, `procd`, `uci-defaults`, LuCI и install/upgrade behavior
5. Использовать [04-build-test-install-debug.md](04-build-test-install-debug.md) как рабочий playbook сборки и отладки
6. Для публикации и дальнейших обновлений читать [05-release-feed-signing-and-updates.md](05-release-feed-signing-and-updates.md)
7. Для старта нового пакета брать шаблоны из [07-package-skeleton.md](07-package-skeleton.md)
8. Для безопасного tmp-based тестирования на живом AX3000T читать [router-ax3000t-safe-test-harness.md](../router-ax3000t-safe-test-harness.md)
9. Перед любым on-device package/service integration на Filogic читать [08-filogic-recovery-write-safety.md](../openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md)
10. При спорных вопросах сверяться с [sources.md](sources.md)

## Структура папки

- [01-lifecycle-and-toolchains.md](01-lifecycle-and-toolchains.md) — полный цикл, выбор SDK/buildroot/feed workflow
- [02-package-layout-and-makefile.md](02-package-layout-and-makefile.md) — anatomy OpenWrt package, `Makefile`, metadata, split packages, versioning
- [03-service-config-and-luci.md](03-service-config-and-luci.md) — `procd`, init scripts, UCI, `uci-defaults`, install/upgrade lifecycle, LuCI
- [04-build-test-install-debug.md](04-build-test-install-debug.md) — локальная сборка, установка на роутер, smoke tests и debug loop
- [05-release-feed-signing-and-updates.md](05-release-feed-signing-and-updates.md) — публикация своего feed, подписи, обновления и maintenance strategy
- [06-cheatsheet.md](06-cheatsheet.md) — короткая шпаргалка
- [07-package-skeleton.md](07-package-skeleton.md) — готовые skeleton-шаблоны для package/core-service/LuCI
- [router-ax3000t-safe-test-harness.md](../router-ax3000t-safe-test-harness.md) — безопасный tmp-based testing lane для живого AX3000T до package install
- [08-filogic-recovery-write-safety.md](../openwrt24-console-knowledge-base/08-filogic-recovery-write-safety.md) — обязательный safety layer перед package/service integration на живом Filogic-роутере
- [openwrt24-appdev-agent-index.json](openwrt24-appdev-agent-index.json) — machine-readable navigation index
- [sources.md](sources.md) — первичные источники

## Быстрый старт

Типовой цикл для нового пакета:

```sh
./scripts/feeds update -a
./scripts/feeds install -a
echo "src-link local /abs/path/to/my-feed" >> feeds.conf
./scripts/feeds update local
./scripts/feeds install -p local myapp
make menuconfig
make package/myapp/compile V=s
```

Потом:

```sh
scp bin/packages/<arch>/local/myapp_*.ipk root@router:/tmp/
ssh root@router opkg install /tmp/myapp_*.ipk
```

Но до package install на этом workspace теперь есть более безопасный first-pass lane для реального AX3000T:

- staging в `/tmp`
- без package DB mutations
- без init/service registration
- с bounded lifetime через watchdog

См. [router-ax3000t-safe-test-harness.md](../router-ax3000t-safe-test-harness.md).

## Минимальная operational-модель

Если упростить до одного тезиса:

1. Подобрать matching SDK или buildroot под конкретный `24.10.x`
2. Оформить пакет с корректным `Makefile`
3. Добавить `/etc/config`, `/etc/init.d`, `conffiles`, если сервис stateful
4. Собирать и тестировать install/remove/upgrade на реальном роутере
5. Публиковать обновления через свой подписанный `opkg` feed
