# Lifecycle и выбор toolchain

Дата подготовки: 2026-04-04

## 1. Базовая линия для OpenWrt 24.xx

Для практической работы эта база принимает `24.10.x` как основную реализацию семейства `24.xx`.

Главные последствия:

- пакетный менеджер на роутере — `opkg`
- формат пакета — `.ipk`
- зависимости и runtime lifecycle нужно проектировать под `procd`, `uci`, `fw4`, `ubus`, `netifd`
- LuCI на `24.10` имеет актуальные JS-based app patterns

## 2. Что такое "полный цикл" для приложения OpenWrt

Рабочий lifecycle выглядит так:

1. Определить форму приложения
2. Выбрать среду сборки
3. Оформить пакет и зависимости
4. Сделать конфигурацию, сервис и init lifecycle
5. При необходимости добавить LuCI-пакет
6. Собрать `.ipk`
7. Установить пакет на роутер
8. Проверить install/remove/upgrade behavior
9. Опубликовать пакет в своем feed
10. Выпускать новые версии и миграции через тот же feed

Если из этой цепочки выпадает хотя бы один слой, приложение перестает быть нормально сопровождаемым.

## 3. Какую build-среду выбирать

### 3.1 SDK — основной выбор для приложений

SDK нужен в большинстве случаев, когда вы:

- собираете userspace package
- не меняете ядро
- не собираете собственный образ целиком
- хотите быстрый цикл упаковки и компиляции

Практически это лучший default для приложения OpenWrt.

### 3.2 Полный buildroot — когда нужен весь OpenWrt tree

Полный `openwrt` tree нужен, если:

- вы меняете базовые пакеты системы
- вам нужна глубокая интеграция в image build
- вы трогаете target-specific вещи
- вы хотите выпускать свой firmware image вместе с приложением

Для простого приложения это часто избыточно, но для platform-integrated решения иногда правильно.

### 3.3 ImageBuilder — не средство разработки приложения

ImageBuilder хорош для сборки образов из уже готовых пакетов.

Он не является основной средой для компиляции нового приложения.

Правильное правило:

- SDK/buildroot компилирует пакет
- ImageBuilder только укладывает уже готовые пакеты в image

### 3.4 Native build на роутере

Это почти никогда не основной production-путь.

Допустимо только для быстрых экспериментов, но не для repeatable package lifecycle.

## 4. Выбор архитектуры и matching environment

SDK и feed должны совпадать с target/subtarget/architecture роутера.

Минимальная проверка на реальном устройстве:

```sh
ubus call system board
grep -E 'DISTRIB_(RELEASE|TARGET|ARCH)' /etc/openwrt_release
opkg print-architecture
uname -m
```

Для Xiaomi AX3000T класса Filogic ожидаемый package architecture обычно:

- `aarch64_cortex-a53`

Но в build-пайплайне доверять нужно не названию модели, а реальным on-device полям.

## 5. Рекомендуемая repository model

Для сопровождения приложений удобна одна из двух схем.

### 5.1 Monorepo feed

В одном репозитории лежат:

- OpenWrt package recipes
- source tarballs or source references
- LuCI package
- release tooling

Это удобно для собственного feed и controlled delivery.

### 5.2 Разделение source repo и packaging repo

Исходники приложения живут отдельно, а feed/packaging отдельно.

Это удобно, если:

- приложение кросс-платформенное
- OpenWrt — только один из таргетов
- нужно отдельно контролировать packaging cadence

## 6. Feed workflow: главный рабочий путь

С точки зрения OpenWrt, локальная разработка пакетов обычно проходит через feeds.

Команды из `scripts/feeds` подтверждают такой workflow:

```sh
./scripts/feeds update -a
./scripts/feeds install -a
./scripts/feeds update <feed>
./scripts/feeds install -p <feed> <package>
```

Ключевая локальная схема:

```text
src-link local /abs/path/to/my-feed
```

Она позволяет подключить локальную директорию как feed, не копируя package recipes вручную в core tree.

## 7. Что считать хорошим lifecycle-design

Хороший OpenWrt-пакет сразу проектируется с учетом:

- package name stability
- зависимостей
- конфигурации в `/etc/config/*`
- persisted config через `conffiles`
- predictable install/upgrade/remove behavior
- отдельного LuCI-пакета, если есть UI
- собственного feed, если планируются обновления после установки

Плохой lifecycle-design выглядит так:

- "сначала закинем бинарник на роутер, потом разберемся"
- отсутствие package recipe
- отсутствие migration strategy
- отсутствие feed и версии
- ручная замена файлов в `/usr/bin` без package manager

## 8. Быстрый выбор между SDK и buildroot

| Сценарий | Что брать |
|----------|-----------|
| userspace daemon/CLI | SDK |
| script-only package | SDK |
| LuCI app | SDK |
| пакет + кастомный image | buildroot |
| kernel module | buildroot |
| глубоко target-specific интеграция | buildroot |
| просто уложить готовые `.ipk` в образ | ImageBuilder |

## 9. Минимальная рекомендуемая стратегия

Для большинства новых проектов под OpenWrt `24.xx`:

1. Скачать matching SDK
2. Подключить локальный `src-link` feed
3. Создать core package
4. Если нужен UI, создать отдельный `luci-app-*`
5. Гонять цикл `compile -> install -> upgrade -> remove`
6. Публиковать `.ipk` через свой feed

