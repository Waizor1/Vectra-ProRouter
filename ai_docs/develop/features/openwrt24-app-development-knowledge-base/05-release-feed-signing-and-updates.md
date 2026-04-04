# Release, feed, подписи и обновления

Дата подготовки: 2026-04-04

## 1. Почему без собственного feed lifecycle неполный

Если пакет ставится только локальным `opkg install /tmp/myapp.ipk`, у вас нет нормального update path.

Собственный feed дает:

- централизованную публикацию новых версий
- `opkg update`
- `opkg list-upgradable`
- `opkg upgrade myapp`
- повторяемую установку после `sysupgrade` и на новых устройствах

## 2. Базовая структура custom feed

Минимум нужен такой набор файлов:

```text
repo/
|- myapp_1.0.0-r1_aarch64_cortex-a53.ipk
|- luci-app-myapp_1.0.0-r1_all.ipk
|- Packages
|- Packages.gz
\- Packages.sig
```

Публикуется это обычно по HTTP или HTTPS.

## 3. Генерация индекса пакетов

OpenWrt build system использует `scripts/ipkg-make-index.sh` для генерации индекса `.ipk`.

Типовой workflow:

```sh
./scripts/ipkg-make-index.sh /path/to/repo > /path/to/repo/Packages
gzip -9c /path/to/repo/Packages > /path/to/repo/Packages.gz
```

Из исходника `ipkg-make-index.sh` видно, что индекс строится по всем `.ipk`, добавляя:

- `Filename`
- `Size`
- `SHA256sum`
- control metadata

## 4. Подпись feed

Для OpenWrt-пакетного мира нужен подписанный индекс и доверенный публичный ключ на роутере.

Типовой OpenWrt flow с `usign`:

```sh
usign -G -s key-build -p key-build.pub -c "Local build key"
usign -S -m /path/to/repo/Packages -s key-build -x /path/to/repo/Packages.sig
```

Практический смысл:

- `-G` — создать пару ключей
- `-S` — подписать файл индекса `Packages`
- `Packages.sig` — detached signature для индекса

На стороне роутера проверка опирается на `opkg-key` и `usign`.

## 5. Как подключить custom feed на роутере

`opkg` package в OpenWrt `24.10` explicitly содержит:

- `/etc/opkg.conf`
- `/etc/opkg/customfeeds.conf`
- `/etc/opkg/keys/`
- `/usr/sbin/opkg-key`

`customfeeds.conf` прямо предназначен для своих feed'ов:

```conf
src/gz myfeed https://example.com/openwrt/myfeed
```

После добавления feed надо импортировать публичный ключ:

```sh
opkg-key add /tmp/key-build.pub
opkg update
```

Потом уже можно:

```sh
opkg install myapp
opkg install luci-app-myapp
```

## 6. Что делает `opkg-key`

По скрипту `opkg-key`:

- `opkg-key add <file>` добавляет trusted public key в `/etc/opkg/keys`
- `opkg-key remove <file>` убирает key
- `opkg-key verify <sigfile> <list>` проверяет индекс против подписи через `usign -V`

Это и есть опорная механика доверия для custom package feed.

## 7. Как выпускать обновления правильно

### 7.1 Packaging-only update

Если поменялись:

- `DEPENDS`
- init script
- `uci-defaults`
- install layout
- LuCI ACL/menu/view packaging

а upstream code тот же, нужно:

- bump `PKG_RELEASE`
- пересобрать `.ipk`
- заново опубликовать feed index

### 7.2 Upstream application update

Если поменялся сам код приложения:

- bump `PKG_VERSION`
- reset или bump `PKG_RELEASE`
- проверить migrations
- прогнать install/upgrade/remove tests
- перегенерировать feed

## 8. Что должно переживать upgrade

По-хорошему после upgrade должны сохраниться:

- пользовательский UCI config
- дополнительные config files из `conffiles`
- осмысленное enabled state сервиса
- LuCI доступность, если UI package тоже обновлен

Чтобы это работало, нужны:

- `conffiles`
- аккуратные migrations
- совместимость между core package и `luci-app-*`

## 9. Migrations при update

Правильный migration design:

- не редактировать config "вручную снаружи package lifecycle"
- versioned schema changes проводить через package-controlled one-shot scripts
- если migration затрагивает runtime behavior, явно проверять reload/start после upgrade

Для OpenWrt-пакетов рабочие инструменты здесь:

- `/etc/uci-defaults/*`
- `postinst-pkg`
- service reload/restart после migration

## 10. Update path на роутере

Когда feed уже настроен:

```sh
opkg update
opkg list-upgradable
opkg upgrade myapp
opkg upgrade luci-app-myapp
```

Для системных пакетов массовый `opkg upgrade` по всему роутеру делать нужно осторожно.

Но для собственных приложений в своем feed это как раз нормальный и ожидаемый lifecycle.

## 11. Совместимость core package и LuCI package

Очень важно не разъезжаться по версиям.

Рабочие правила:

- `luci-app-myapp` должен зависеть от `myapp`
- schema changes в UCI должны быть синхронизированы между core и UI
- при incompatible change лучше выпускать оба пакета вместе

## 12. Что делать после firmware sysupgrade

Даже если пакетный lifecycle идеален, firmware upgrade — отдельный слой.

Практическая стратегия:

- держать пакет доступным в custom feed
- сохранять конфиг в `conffiles`
- перед firmware upgrade иметь backup
- после прошивки иметь возможность сделать `opkg update && opkg install myapp`

Опционально можно использовать package-list retention стратегии, но собственный feed все равно остается главным надежным источником повторной установки.

## 13. Release checklist

- Верно выставлены `PKG_VERSION` / `PKG_RELEASE`
- Core package и LuCI package совместимы
- Fresh install проверен
- Upgrade проверен
- Remove проверен
- `conffiles` заданы
- Migrations отработали
- Собран новый `Packages`
- Пересобран `Packages.gz`
- Переподписан `Packages.sig`
- Public key уже известен целевым роутерам

