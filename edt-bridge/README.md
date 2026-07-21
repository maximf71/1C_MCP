# Нативный MCP bridge для 1C:EDT

Опциональный Eclipse/OSGi-плагин для EDT 2025.2.6. Он открывает только loopback-сокет, создаёт временный токен в `bridge.json` и закрепляется за одним проектом EDT.

Возможности:

- статус и структура метаданных через модель EDT;
- список, чтение и поиск BSL-модулей;
- диагностика и content assist в позиции курсора;
- двухфазное клонирование метаданных;
- импорт управляемых XML-исходников EPF/ERF в проекты с заданным префиксом.

## Сборка

Требуются JDK 17 и Maven 3.9+:

```powershell
$env:JAVA_HOME = 'C:\Program Files\Java\jdk-17'
mvn verify
```

Основной JAR: `bundles\org.example.ui\target\com.codex.onec.edt.mcp-1.0.0-SNAPSHOT.jar`.

## Обязательная настройка EDT

После `-vmargs` в `1cedt.ini` задайте хотя бы:

```text
-Donec.mcp.project=ИмяПроектаEDT
```

Дополнительные параметры:

```text
-Donec.mcp.port=17831
-Donec.mcp.runtimeDir=D:\MCP-Work\edt-runtime
-Donec.mcp.externalRoot=D:\MCP-Work\external-objects
-Donec.mcp.externalProjectPrefix=CodexExt_
```

Без `onec.mcp.project` bridge намеренно не запускается. Полная инструкция: [../docs/INSTALL.md](../docs/INSTALL.md).

## Безопасность

- сервер привязан к loopback-интерфейсу;
- токен генерируется при старте EDT и сравнивается constant-time;
- размер запроса ограничен;
- BSL-пути разрешены только внутри `src` фиксированного проекта;
- импорт внешних объектов ограничен корневым каталогом и префиксом проекта;
- `bridge.json` удаляется при штатном завершении EDT.

## Происхождение и лицензия

Проект является производной работой от [1C-Company/dt-example-plugins](https://github.com/1C-Company/dt-example-plugins) и распространяется по EPL-2.0. См. `LICENSE`.
