# Установка

Ниже описана установка с нуля на Windows. Для основной схемы достаточно DitriX EDT-MCP и Go-обёртки. Собственный `edt-bridge` нужен только для дополнительных нативных инструментов и управляемых проектов EPF/ERF.

## 1. Предварительные требования

- законно установленная 1C:EDT;
- открывающийся в EDT проект конфигурации;
- PowerShell 5.1 или 7;
- Git;
- Go 1.25+ для сборки `mcp-wrapper`;
- JDK 17 и Maven 3.9+ только для сборки опционального `edt-bridge`;
- Codex Desktop или другой MCP-клиент с поддержкой STDIO.

Проверенная комбинация — EDT 2025.2.6.4 и платформа 1С 8.3.27. Более новые версии требуют повторного прогона тестов.

## 2. Клонирование

```powershell
git clone https://github.com/maximf71/1C_MCP.git C:\Tools\1C_MCP
Set-Location C:\Tools\1C_MCP
```

Путь может быть другим. В примерах ниже замените `C:\Tools\1C_MCP` на фактический каталог репозитория.

## 3. Установка официального DitriX EDT-MCP

Рекомендуемый способ:

1. Откройте EDT.
2. Выберите **Справка → Установить новое ПО / Help → Install New Software**.
3. Добавьте update site `https://ditrixnew.github.io/EDT-MCP/`.
4. Выберите **EDT MCP Server Feature**.
5. Завершите установку и полностью перезапустите EDT.
6. Откройте **Окно → Настройки → MCP Server / Window → Preferences → MCP Server**.
7. Установите порт `8765`, включите автозапуск и нужные группы инструментов.

Для снимков управляемых форм добавьте после строки `-vmargs` в файл `1cedt.ini`:

```text
-DnativeFormBufferedLayoutRender=true
```

После правки снова полностью перезапустите EDT. Это требование самого DitriX EDT-MCP: без флага снимок формы может быть серым или пустым.

Проверка:

```powershell
$health = Invoke-RestMethod http://127.0.0.1:8765/health
$health | Format-List
```

Ожидается `ready = true`. Для воспроизводимости проекта использовалась версия `2.8.1`; новые версии сначала проверяйте на тестовом workspace.

## 4. Сборка STDIO-обёртки

```powershell
Set-Location C:\Tools\1C_MCP\mcp-wrapper
go version
.\build.ps1 -Version 0.5.0
```

Сценарий сначала выполняет `go test ./...`, затем создаёт:

```text
mcp-wrapper\dist\mcp-1c-analog.exe
mcp-wrapper\dist\mcp-1c-analog-0.5.0.exe
```

Проверьте версию:

```powershell
.\dist\mcp-1c-analog.exe --version
```

## 5. Регистрация в Codex

Откройте пользовательский `config.toml` Codex и добавьте блок:

```toml
[mcp_servers.onec_edt]
command = "C:\\Tools\\1C_MCP\\mcp-wrapper\\dist\\mcp-1c-analog.exe"
args = [
  "--ditrix-edt-url", "http://127.0.0.1:8765",
  "--ditrix-project", "ИмяПроектаEDT"
]
startup_timeout_sec = 60
tool_timeout_sec = 300
```

`ИмяПроектаEDT` — именно имя проекта в навигаторе EDT, а не путь к каталогу.

Если одновременно нужен доступ к информационной базе через Конфигуратор, добавьте фиксированные параметры:

```toml
args = [
  "--platform", "C:\\Program Files\\1cv8\\8.3.27.1644\\bin\\1cv8.exe",
  "--infobase", "D:\\1C_Bases\\MyBase",
  "--work-dir", "D:\\MCP-Work\\my-base",
  "--ditrix-edt-url", "http://127.0.0.1:8765",
  "--ditrix-project", "ИмяПроектаEDT"
]
```

Учётные данные не записывайте в TOML. Перед запуском Codex задайте их в окружении процесса:

```powershell
$env:ONEC_DB_USER = 'Администратор'
$env:ONEC_DB_PASSWORD = '<пароль>'
```

После изменения конфигурации полностью перезапустите Codex. Уже запущенный процесс не перечитывает бинарник и набор MCP-инструментов автоматически.

## 6. Опциональный нативный EDT bridge

DitriX EDT-MCP покрывает обычную работу с проектом. Собственный bridge добавляет отдельный локальный контур и нужен только тем, кому требуются его дополнительные функции.

Сборка:

```powershell
$env:JAVA_HOME = 'C:\Program Files\Java\jdk-17'
Set-Location C:\Tools\1C_MCP\edt-bridge
mvn -DskipTests package
```

JAR создаётся по адресу:

```text
edt-bridge\bundles\org.example.ui\target\com.codex.onec.edt.mcp-1.0.0-SNAPSHOT.jar
```

Скопируйте JAR в отдельную dropins-структуру EDT:

```text
<EDT_HOME>\dropins\com.codex.onec.edt.mcp\plugins\com.codex.onec.edt.mcp-1.0.0-SNAPSHOT.jar
```

После `-vmargs` в `<EDT_HOME>\1cedt.ini` добавьте:

```text
-Donec.mcp.project=ИмяПроектаEDT
-Donec.mcp.runtimeDir=D:\MCP-Work\edt-runtime
-Donec.mcp.externalRoot=D:\MCP-Work\external-objects
-Donec.mcp.externalProjectPrefix=CodexExt_
```

Создайте указанные каталоги, перезапустите EDT и проверьте появление файла `D:\MCP-Work\edt-runtime\bridge.json`. Файл содержит временный токен: не публикуйте его и не копируйте между машинами.

Чтобы включить bridge в обёртке, добавьте:

```toml
"--edt-bridge", "D:\\MCP-Work\\edt-runtime\\bridge.json",
"--external-objects-root", "D:\\MCP-Work\\external-objects"
```

## 7. Обновление

1. Сделайте резервную копию проекта EDT и базы.
2. Получите изменения Git в отдельной ветке.
3. Повторите тесты из [TESTING.md](TESTING.md).
4. Пересоберите EXE/JAR.
5. Полностью перезапустите EDT и Codex.

Не заменяйте JAR работающего EDT и не применяйте изменения сразу к рабочей базе без проверки на копии.
