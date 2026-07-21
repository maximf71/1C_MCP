# Настройка рабочего места

## Быстрый старт

Запустите интерактивный мастер:

```powershell
mcp-1c-analog.exe setup
```

Либо создайте профиль без диалога:

```powershell
mcp-1c-analog.exe setup `
  --id my_base `
  --name "Моя база" `
  --base-kind file `
  --infobase "D:\1C_Bases\MyBase" `
  --platform "C:\Program Files\1cv8\8.3.27.1644\bin\1cv8.exe" `
  --base-url "http://localhost:8080/hs/mcp-1c" `
  --dump "D:\MCP-Work\my-base\dump" `
  --edt-bridge "D:\MCP-Work\edt-runtime\bridge.json" `
  --ditrix-url "http://127.0.0.1:8765/mcp" `
  --ditrix-project "ИмяПроектаEDT"
```

`setup` устанавливает конфигурационное расширение `MCP_HTTPService`, сохраняет
профиль и добавляет в Codex два жестко закрепленных сервера:
`my_base_db` и `my_base_edt`. Перед изменением `config.toml` создается резервная
копия. Публикация HTTP-сервиса через Apache/IIS остается отдельным шагом
платформы 1С; `profile check` проверяет, что опубликованный URL отвечает.

Для уже установленного расширения используйте `--skip-extension`. Для генерации
только профиля без изменения Codex — `--skip-codex`.

## Перенос на другой компьютер

```powershell
mcp-1c-analog.exe profile export my_base D:\Transfer\my_base.json
mcp-1c-analog.exe profile import D:\Transfer\my_base.json
mcp-1c-analog.exe profile check my_base
```

Экспорт не содержит паролей. Учетные данные задаются переменными окружения,
имена которых сохранены в профиле. После импорта исправьте локальные пути
повторным `setup --id my_base ...`; управляемый блок Codex будет заменен
атомарно.

## Несколько баз

Создавайте отдельный профиль для каждой базы. Один процесс MCP никогда не
переключает базу по аргументам инструмента. Это исключает случайное обращение к
другой информационной базе или EDT-проекту.

```powershell
mcp-1c-analog.exe profile list
mcp-1c-analog.exe profile check my_base
mcp-1c-analog.exe index build --profile my_base
mcp-1c-analog.exe analyze --profile my_base --format sarif
```

Профили находятся в `%LOCALAPPDATA%\mcp-1c\profiles`. Кэши, память и рабочие
файлы разделены по идентификатору профиля.
