---
type: workflow
updated: 2026-04-05
tags:
  - workflow
  - agents
  - obsidian
---

# Agent Workflow

> [!warning]
> Работа по задаче не считается завершенной, пока в `ProRouter/` не обновлены статус и следы сессии.

## Start Protocol

1. Прочитать [[Home]], [[00 Dashboard/Stage Board|Stage Board]] и [[00 Dashboard/Repo Map|Repo Map]].
2. Открыть заметки затронутых модулей из `02 Modules/`.
3. Открыть актуальную дневную заметку в `04 Sessions/Daily/`.
4. Если структура репозитория недавно менялась, запустить `scripts/Sync-ProRouterVault.ps1`.

## During Work

- Если меняются границы модуля, обновить соответствующую заметку в `02 Modules/`.
- Если меняется стадия зрелости или confidence, обновить [[00 Dashboard/Stage Board|Stage Board]].
- Если принято архитектурное или процессное решение, зафиксировать его в `03 Decisions/`.

## Finish Protocol

1. Обновить затронутые заметки модуля.
2. Если изменилась структура репозитория, запустить `scripts/Sync-ProRouterVault.ps1`.
3. Добавить status entry в дневную заметку через `scripts/Add-ProRouterStatusEntry.ps1`.
4. Только после этого отдавать финальный ответ пользователю.

## Delegation Rule

- Любой агент должен сначала читать `ProRouter/`.
- Если работа идет через forked sub-agent или параллельные агенты, итоговую запись в основной vault делает главный агент в основном workspace.
- Sub-agent не считается завершенным, пока его результат не консолидирован в `ProRouter/`.

## Commands

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Sync-ProRouterVault.ps1
```

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\Add-ProRouterStatusEntry.ps1 -Summary "Implemented X" -Modules "Web Control Plane","Router Agent" -NextSteps "Verify deploy flow"
```
