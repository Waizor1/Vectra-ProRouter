# Build, test, install и debug loop

Дата подготовки: 2026-04-04

## 1. Базовый local development loop

Самый практичный цикл для OpenWrt-приложения:

1. Подключить локальный feed
2. Установить package metadata в tree
3. Собрать нужный пакет
4. Перенести `.ipk` на роутер
5. Проверить install/start/config/UI
6. Повторить цикл после правок

## 2. Подключение локального feed

Типовой workflow:

```sh
echo "src-link local /abs/path/to/my-feed" >> feeds.conf
./scripts/feeds update local
./scripts/feeds install -p local myapp
```

Если есть LuCI package:

```sh
./scripts/feeds install -p local luci-app-myapp
```

## 3. Выбор пакета в конфигурации

После установки feed metadata:

```sh
make menuconfig
```

Дальше:

- выбрать `myapp`
- выбрать `luci-app-myapp`, если нужен UI
- при необходимости выполнить `make defconfig`

## 4. Сборка пакета

Главная команда для точечной сборки:

```sh
make package/myapp/compile V=s
```

Полезные варианты:

```sh
make package/myapp/clean
make package/myapp/prepare V=s
make package/myapp/configure V=s
make package/myapp/compile V=s
make package/luci-app-myapp/compile V=s
```

Практика:

- `V=s` — основной verbose mode
- начинать с `clean`, если непонятно, не залип ли package state

## 5. Где искать результаты сборки

Для `.ipk` пакетов артефакты обычно оказываются в:

```text
bin/packages/<arch>/<feed>/
```

Для core-пакета и LuCI-пакета там будут отдельные `.ipk`.

## 6. Установка на роутер

Типовой путь:

```sh
scp bin/packages/<arch>/local/myapp_*.ipk root@router:/tmp/
scp bin/packages/<arch>/local/luci-app-myapp_*.ipk root@router:/tmp/
ssh root@router opkg install /tmp/myapp_*.ipk
ssh root@router opkg install /tmp/luci-app-myapp_*.ipk
```

Если core package уже установлен и нужно обновление локального файла:

```sh
ssh root@router opkg upgrade /tmp/myapp_*.ipk
```

На практике многие сначала делают:

```sh
ssh root@router opkg remove myapp
ssh root@router opkg install /tmp/myapp_*.ipk
```

Но для реального lifecycle обязательно надо тестировать именно upgrade path, а не только reinstall.

## 7. Что проверять сразу после установки

### 7.1 Сервис

```sh
service myapp status
ubus call service list '{ "name": "myapp", "verbose": true }'
logread -e myapp
ps w | grep myapp
```

### 7.2 Конфиг

```sh
uci show myapp
uci changes
cat /etc/config/myapp
```

### 7.3 LuCI

```sh
opkg status luci-app-myapp
logread -e rpcd
logread -e uhttpd
```

Если доступен браузер, открыть страницу LuCI и проверить:

- меню появилось
- форма открывается
- чтение UCI работает
- запись UCI работает

## 8. Debug loop на роутере

Если пакет поставился, но поведение неправильное:

```sh
logread -f
logread -e myapp
logread -e rpcd
logread -e uhttpd
service myapp restart
ubus call service list '{ "name": "myapp", "verbose": true }'
```

Для config-trigger проблем:

```sh
uci changes myapp
uci commit myapp
reload_config
service myapp status
```

## 9. Что тестировать кроме fresh install

### 9.1 Upgrade

Проверить:

- сохраняется ли `/etc/config/myapp`
- выполняются ли migrations
- сервис корректно перезапускается
- LuCI остается совместимым с новой schema

Типовой тест:

```sh
opkg install /tmp/myapp_old.ipk
opkg upgrade /tmp/myapp_new.ipk
uci show myapp
service myapp status
logread -e myapp
```

### 9.2 Remove

Проверить:

- корректно ли выключается сервис
- не остаются ли битые symlink/acl/menu artifacts
- не ломается ли LuCI после удаления UI package

```sh
opkg remove luci-app-myapp
opkg remove myapp
```

### 9.3 Reinstall

Проверить:

- идempotent ли install path
- не конфликтуют ли старые `uci-defaults` assumptions

## 10. Когда использовать `USE_SOURCE_DIR`

Если package recipe уже написан и нужно быстро гонять compile loop на локальных исходниках:

```sh
make package/myapp/compile V=s USE_SOURCE_DIR=/abs/path/to/myapp-src
```

Это удобно для активной разработки, но release verification все равно должен проходить без привязки к локальному абсолютному пути.

## 11. Частые причины проблем

| Симптом | Что проверять |
|---------|----------------|
| пакет не собирается | `DEPENDS`, build helper, `Build/Compile`, target SDK |
| пакет собрался, но пустой | `Package/<name>/install` |
| сервис не стартует | init script, `procd_set_param command`, file permissions |
| upgrade ломает конфиг | `conffiles`, `uci-defaults` migration logic |
| LuCI не видит конфиг | базовый `/etc/config/<name>` не создан |
| LuCI страница есть, но запись не работает | ACL JSON |
| пакет не обновляется через repo | неверный feed index/signature/version bump |

## 12. Минимальный smoke test checklist

- `.ipk` реально собирается
- `opkg install` проходит без ошибок
- сервис стартует
- `uci show <name>` отдает ожидаемую схему
- `opkg remove` не оставляет broken runtime state
- `opkg upgrade` сохраняет конфиг и поднимает сервис обратно
- `luci-app-*` работает отдельно от core package

