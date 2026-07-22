# Проверка

Для версии 0.6.0 дополнительно выполните `go test -race ./...` там, где установлен C-компилятор для CGO. На Windows без GCC используйте повторный прогон `go test -count=5 ./...`. Затем проверьте сценарии из раздела «Приёмочная проверка» в [TOOLS_0_6.md](TOOLS_0_6.md).

## Go-обёртка

```powershell
Set-Location .\mcp-wrapper
go test ./...
.\build.ps1 -Version 0.6.0
.\dist\mcp-1c-analog.exe --version
```

Ожидается успешное завершение всех пакетов и строка `mcp-1c-analog 0.6.0`.

## DitriX EDT-MCP

При запущенном EDT:

```powershell
$health = Invoke-RestMethod http://127.0.0.1:8765/health
if (-not $health.ready) { throw 'EDT-MCP is not ready' }
$health
```

В EDT не должно быть активного построения проекта. Если оно идёт, дождитесь завершения: обёртка повторяет временные ошибки построения не более 30 секунд.

## EDT bridge

```powershell
$env:JAVA_HOME = 'C:\Program Files\Java\jdk-17'
Set-Location .\edt-bridge
mvn verify
```

После установки и перезапуска EDT проверьте, что `bridge.json` существует только при запущенном EDT и содержит loopback-адрес (`127.0.0.1` или `localhost`).

## Минимальный smoke-тест MCP

После регистрации сервера запросите через MCP:

- `get_configuration_info` — ответ должен приходить из модели EDT;
- `get_object_structure` для существующего объекта — должен использовать полный `get_metadata_details`;
- `get_configuration_status` — проект должен быть открыт и синхронизирован.

В журнале не должно быть попыток обращения к устаревшему `localhost:8080`.
