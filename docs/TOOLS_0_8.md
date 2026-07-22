# Крупные подсистемы версии 0.8.0

Версия 0.8.0 добавляет четыре самостоятельных контура: `diagnostics`, `vanessa`, расширенный `edit_metadata` и `update_configuration`. Все пути задаются при запуске MCP и не могут быть заменены аргументами вызова.

## Технологический журнал и диагностика

`diagnostics` объединяет замеры EDT (`measureStart`, `measureStop`, `measureResults`, `measureCoverage`, `measureCallers`), технологический журнал (`techlogEnable`, `techlogAnalyze`, `techlogDisable`, `techlogClear`) и журнал регистрации фиксированной базы (`eventlogRead`, `eventlogSummary`).

```powershell
--techlog-config 'C:\Program Files\1cv8\conf\logcfg.xml' `
--techlog-root 'D:\1C-Logs\techlog'
```

`techlogEnable` поддерживает пресеты `performance`, `exceptions`, `locks`, `server`, `all`, порог `minimumDurationMs` и срок хранения `historyHours`. Перед заменой существующего `logcfg.xml` сохраняется исходная копия; `techlogDisable` восстанавливает её. Изменение конфигурации и очистка требуют `confirm=true`. Анализ читает только `.log` под закреплённым корнем и возвращает самые долгие события, свойства, строки и сводку по типам.

## Vanessa Automation

Необходима отдельно установленная Vanessa Automation. Репозиторий не включает её EPF и внешние компоненты.

```powershell
--vanessa-platform 'C:\Program Files\1cv8\8.3.27.1688\bin\1cv8.exe' `
--vanessa-infobase 'D:\Bases\TestBase' `
--vanessa-runner 'D:\Tools\vanessa-automation\vanessa-automation.epf' `
--vanessa-features-root 'D:\Project\features' `
--vanessa-steps-root 'D:\Tools\vanessa-automation\features'
```

Операции: `status`, автономный индекс `steps`, проверка `checkSyntax` и подтверждаемый `run`. Запуск использует официальный пакетный контракт `StartFeaturePlayer;VAParams=...`; фильтры тегов, JUnit и снимки при ошибке пишутся в закреплённый рабочий каталог. Путь фичи всегда относительный к `--vanessa-features-root`.

## Полное дерево edit_metadata

Фасад публикует операции из 11 областей эталонного `edit_metadata`: объекты и реквизиты, специализированные объекты, формы, макеты, командный интерфейс, расширения, HTTP-сервисы и полный набор операций СКД. `help` возвращает фактически зарегистрированный список — более 140 имён.

Чтение выполняется сразу. Все операции изменения по умолчанию возвращают dry-run; выполнение требует `confirm=true`. Имя проекта всегда подставляет обёртка. Конкретная операция зависит от возможностей установленного EDT backend; если backend её не поддерживает, возвращается явная ошибка без небезопасного файлового обхода.

## Обновление и управляемое объединение

```powershell
--configuration-source-root 'D:\1C-Updates'
```

`source` и `ancestor` принимаются только относительно этого корня. Доступны `prepareSource`, `compare`, `differences`, `merge`, `updateVendor`, `exportDifferences`, `replayCustomizations`, `status`, `cancel`, `cleanupSources`.

Если EDT backend предоставляет нативный `update_configuration`, вызов передаётся ему с фиксированным проектом. В EDT 2025.2 используется защищённый режим дерева исходников:

1. `compare` рассчитывает SHA-256 проекта и источника, формирует неизменяемый `planId` и классифицирует файлы как `onlyInMain`, `onlyInOther`, `changed`.
2. `differences` возвращает постраничный список и фильтр `scope`.
3. `merge` принимает правила `взятьИзНовой`, `оставитьСвою`, `объединитьПриоритетНовой`, `объединитьПриоритетСвоей` (или английские алиасы). По умолчанию — dry-run и сохранение своей версии.
4. Перед записью повторно проверяется отпечаток проекта и создаётся полный снимок исходников.

Каталог или EDT-проект работает без дополнительных компонентов. Полный `.cf` сначала преобразуется в XML через временную файловую базу и требует `--platform` и `--infobase`.

Важно: файловый режим EDT 2025.2 не переносит сведения поддержки поставщика и не выдаётся за штатное объединение EDT 2026.1. Для сохранения поддержки используйте нативный backend EDT 2026.1+; `help` показывает текущий режим и `provider_support_metadata`.

## Приёмочная проверка

```powershell
Set-Location .\mcp-wrapper
go test ./...
go vet ./...
.\build.ps1 -Version 0.8.0
.\dist\mcp-1c-analog.exe --version
```

Безопасный live-smoke при запущенном EDT: `diagnostics status`, `vanessa status`, `update_configuration help`, `edit_metadata help`. Эти вызовы ничего не изменяют.
