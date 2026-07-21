# mcp-wrapper

Локальный STDIO MCP-сервер на Go для 1С. Процесс закрепляется за одной заданной целью и может объединять:

- модель проекта из DitriX EDT-MCP;
- опциональный нативный `edt-bridge`;
- XML-выгрузку и операции Конфигуратора для фиксированной информационной базы;
- безопасный двухфазный конвейер изменений метаданных.

## Сборка

Требуется Go 1.25 или новее в `PATH`.

```powershell
.\build.ps1 -Version 0.5.0
```

Сценарий выполняет тесты и создаёт `dist\mcp-1c-analog.exe`.

## Быстрый запуск для EDT

```powershell
.\dist\mcp-1c-analog.exe `
  --ditrix-edt-url http://127.0.0.1:8765 `
  --ditrix-project 'ИмяПроектаEDT'
```

Параметры `--ditrix-edt-url` и `--ditrix-project` задаются только вместе. Для доступа к базе учётные данные читаются из `ONEC_DB_USER` и `ONEC_DB_PASSWORD`; передавать пароль в аргументах MCP-инструмента не требуется.

Полная установка и пример конфигурации Codex: [../docs/INSTALL.md](../docs/INSTALL.md). Карта функций: [docs/FUNCTIONAL_PARITY.md](docs/FUNCTIONAL_PARITY.md).

## Исправления интеграции EDT

В версии 0.5.0:

- `get_configuration_info` использует нативный `get_configuration_properties` DitriX EDT-MCP;
- `get_object_structure` использует `get_metadata_details` с `full=true`;
- исходный MCP-ответ сохраняется без преобразования в устаревший HTTP-формат;
- временные ошибки `Project is building` и `derived data not complete` повторяются до 30 секунд с интервалом 500 мс;
- проксируемые инструменты жёстко привязаны к настроенному EDT-проекту.

## Лицензия

MIT. См. `LICENSE` и `THIRD_PARTY_NOTICES.md`.
