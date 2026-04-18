---
type: hub
project: Vectra-ProRouter
updated: 2026-04-05
tags:
  - hub
  - codex
  - obsidian
---

# Vectra ProRouter

Локальный Obsidian vault для долговременной памяти по проекту. Здесь держим не исходники, а сжатую рабочую картину: структура репозитория, стадии модулей, решения, сессии и ссылки на исходные файлы.

## Быстрый вход

- [[00 Dashboard/Stage Board|Stage Board]]
- [[00 Dashboard/Repo Map|Repo Map]]
- [[00 Dashboard/Agent Workflow|Agent Workflow]]
- [[03 Decisions/ADR Index|ADR Index]]
- [[04 Sessions/Daily/2026-04-05|Daily Session]]

## Модули

- [[02 Modules/Knowledge Base and Runbooks|Knowledge Base and Runbooks]]
- [[02 Modules/Web Control Plane|Web Control Plane]]
- [[02 Modules/Shared Contracts|Shared Contracts]]
- [[02 Modules/Shared Database|Shared Database]]
- [[02 Modules/Router Agent|Router Agent]]
- [[02 Modules/LuCI Controller Package|LuCI Controller Package]]
- [[02 Modules/Deployment Stack|Deployment Stack]]
- [[02 Modules/Source Mirrors|Source Mirrors]]

## Как этим пользоваться

1. В начале работы открывай [[00 Dashboard/Agent Workflow|Agent Workflow]] и заметки нужных модулей.
2. После заметного изменения структуры запускай `python3 ./scripts/Sync-ProRouterVault.py`.
3. После существенной работы обновляй заметку модуля, [[00 Dashboard/Stage Board]] и дневную заметку через `python3 ./scripts/Add-ProRouterStatusEntry.py`.
4. Архитектурные или процессные решения фиксируй в `03 Decisions/`.

## Live Views

![[00 Dashboard/Module Status.base#Modules]]

![[00 Dashboard/Session Feed.base#Daily Sessions]]

![[00 Dashboard/Decision Register.base#Decisions]]

## Границы

- Источник истины по поведению кода: сами исходники и ранбуки.
- Этот vault хранит сжатое состояние проекта и навигацию по нему.
- Статусы в заметках основаны на файловом состоянии репозитория, если явно не указано иное.
