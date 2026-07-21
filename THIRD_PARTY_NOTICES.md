# Сторонние компоненты

## 1C Company EDT example plugins

Каталог `edt-bridge` создан на основе официального примера плагинов EDT:
https://github.com/1C-Company/dt-example-plugins

Он распространяется отдельно на условиях Eclipse Public License 2.0. Полный
текст лицензии сохранён в `edt-bridge/LICENSE`. Исходные уведомления и история
авторства должны сохраняться при распространении производной работы.

## DitriX EDT-MCP

Проект интегрируется с DitriX EDT-MCP, но не включает его JAR или исходный код:
https://github.com/DitriXNew/EDT-MCP

Устанавливайте официальный релиз самостоятельно и соблюдайте лицензию upstream.

## feenlace/mcp-1c

Части расширения 1С и установщика в `mcp-wrapper` адаптированы из публичного
MIT-проекта https://github.com/feenlace/mcp-1c. Точный commit и уведомления
приведены в `mcp-wrapper/THIRD_PARTY_NOTICES.md`.

## Model Context Protocol Go SDK

STDIO-транспорт использует `github.com/modelcontextprotocol/go-sdk` версии,
закреплённой в `mcp-wrapper/go.mod`, на условиях лицензии upstream.

## 1C:Enterprise и 1C:EDT

Платформа 1С и 1C:EDT не входят в репозиторий. Для запуска требуются законно
установленные экземпляры.
