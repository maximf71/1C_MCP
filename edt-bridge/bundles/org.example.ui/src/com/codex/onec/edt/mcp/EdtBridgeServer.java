package com.codex.onec.edt.mcp;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.Closeable;
import java.io.EOFException;
import java.io.IOException;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ThreadFactory;
import java.util.concurrent.atomic.AtomicInteger;

import org.eclipse.core.runtime.IStatus;
import org.eclipse.core.runtime.Status;

import com.codex.onec.edt.mcp.EdtMetadataService.BridgeException;
import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;

final class EdtBridgeServer implements Closeable {
    private static final int DEFAULT_PORT = 17831;
    private static final int MAX_HEADER_LINE = 8192;
    private static final int MAX_BODY = 1024 * 1024;

    private final EdtMetadataService metadata;
    private final EdtBslService bsl;
    private final EdtExternalObjectService externalObjects;
    private final EdtInfobaseService infobases;
    private final Gson gson = new Gson();
    private final ExecutorService clients = Executors.newCachedThreadPool(new DaemonThreadFactory());
    private final String token = createToken();
    private final Path runtimeDirectory = Path.of(System.getProperty("onec.mcp.runtimeDir",
        Path.of(System.getProperty("user.home"), ".onec-edt-mcp").toString()));
    private final Path bridgeFile = runtimeDirectory.resolve("bridge.json");
    private ServerSocket socket;
    private Thread acceptThread;

    EdtBridgeServer(EdtMetadataService metadata, EdtBslService bsl,
        EdtExternalObjectService externalObjects, EdtInfobaseService infobases) {
        this.metadata = metadata;
        this.bsl = bsl;
        this.externalObjects = externalObjects;
        this.infobases = infobases;
    }

    void start() throws IOException {
        int port = Integer.getInteger("onec.mcp.port", DEFAULT_PORT);
        socket = new ServerSocket(port, 32, InetAddress.getLoopbackAddress());
        socket.setReuseAddress(true);
        writeBridgeFile(socket.getLocalPort());
        acceptThread = new Thread(this::acceptLoop, "onec-edt-mcp-accept");
        acceptThread.setDaemon(true);
        acceptThread.start();
    }

    private void acceptLoop() {
        while (socket != null && !socket.isClosed()) {
            try {
                Socket client = socket.accept();
                client.setSoTimeout(30_000);
                clients.execute(() -> handle(client));
            } catch (IOException e) {
                if (socket != null && !socket.isClosed()) {
                    Activator.getDefault().getLog().log(
                        new Status(IStatus.ERROR, Activator.PLUGIN_ID, "EDT bridge accept failed", e));
                }
            }
        }
    }

    private void handle(Socket client) {
        try (client;
            BufferedInputStream input = new BufferedInputStream(client.getInputStream());
            BufferedOutputStream output = new BufferedOutputStream(client.getOutputStream())) {
            try {
                HttpRequest request = readRequest(input);
                if (!authorized(request.headers().get("authorization"))) {
                    writeResponse(output, 401, errorBody("Unauthorized"));
                    return;
                }
                Map<String, Object> response = dispatch(request);
                writeResponse(output, 200, response);
            } catch (BridgeException e) {
                writeResponse(output, e.status(), errorBody(e.getMessage()));
            } catch (Exception e) {
                writeResponse(output, 500, errorBody(safeMessage(e)));
            }
        } catch (IOException ignored) {
            // The peer has disconnected or EDT is shutting down.
        }
    }

    private Map<String, Object> dispatch(HttpRequest request) {
        JsonObject body = request.body().isBlank() ? new JsonObject()
            : JsonParser.parseString(request.body()).getAsJsonObject();
        String path = request.path();
        if ("GET".equals(request.method()) && "/health".equals(path)) {
            Map<String, Object> health = metadata.health();
            health.put("infobase_management", true);
            return health;
        }
        if (!"POST".equals(request.method())) {
            throw new BridgeException(405, "Method not allowed");
        }
        return switch (path) {
        case "/list" -> metadata.listObjects(optionalString(body, "type"));
        case "/inspect" -> metadata.inspect(requiredString(body, "type"), requiredString(body, "name"));
        case "/prepare-clone" -> metadata.prepareClone(requiredString(body, "type"),
            requiredString(body, "source_name"), requiredString(body, "target_name"));
        case "/apply-clone" -> metadata.applyClone(requiredString(body, "plan_id"));
        case "/verify" -> metadata.verify(requiredString(body, "type"), requiredString(body, "name"));
        case "/discard-plan" -> metadata.discard(requiredString(body, "plan_id"));
        case "/bsl/list" -> bsl.listModules(optionalString(body, "contains"), optionalInt(body, "limit"));
        case "/bsl/read" -> bsl.readModule(requiredString(body, "module_path"),
            optionalInt(body, "start_line"), optionalInt(body, "end_line"));
        case "/bsl/search" -> bsl.searchCode(requiredString(body, "query"),
            optionalString(body, "path_contains"), optionalInt(body, "limit"));
        case "/bsl/diagnostics" -> bsl.diagnostics(optionalString(body, "module_path"),
            optionalInt(body, "limit"));
        case "/bsl/content-assist" -> bsl.contentAssist(requiredString(body, "module_path"),
            requiredInt(body, "line"), requiredInt(body, "column"), optionalString(body, "contains"),
            optionalInt(body, "limit"), optionalBoolean(body, "include_documentation"));
        case "/external/import-xml" -> externalObjects.importXml(requiredString(body, "project_name"),
            requiredString(body, "source_xml"), optionalString(body, "platform_version"));
        case "/infobases/list" -> infobases.list();
        case "/infobases/bind" -> infobases.bind(requiredString(body, "infobase_name"),
            optionalBoolean(body, "register"), optionalString(body, "base_kind"),
            optionalString(body, "file_path"), optionalString(body, "server"),
            optionalString(body, "reference"), optionalString(body, "version"),
            optionalBoolean(body, "already_synchronized"), optionalBoolean(body, "set_default"),
            optionalBoolean(body, "confirm"));
        case "/infobases/unbind" -> infobases.unbind(requiredString(body, "infobase_name"),
            optionalBoolean(body, "unregister"), optionalBoolean(body, "confirm"));
        default -> throw new BridgeException(404, "Endpoint not found");
        };
    }

    private static int requiredInt(JsonObject body, String name) {
        if (!body.has(name) || !body.get(name).isJsonPrimitive() || !body.get(name).getAsJsonPrimitive().isNumber()) {
            throw new BridgeException(400, name + " is required and must be a number");
        }
        return body.get(name).getAsInt();
    }

    private static int optionalInt(JsonObject body, String name) {
        return body.has(name) && body.get(name).isJsonPrimitive() && body.get(name).getAsJsonPrimitive().isNumber()
            ? body.get(name).getAsInt() : 0;
    }

    private static boolean optionalBoolean(JsonObject body, String name) {
        return body.has(name) && body.get(name).isJsonPrimitive() && body.get(name).getAsJsonPrimitive().isBoolean()
            && body.get(name).getAsBoolean();
    }

    private HttpRequest readRequest(BufferedInputStream input) throws IOException {
        String requestLine = readLine(input);
        String[] parts = requestLine.split(" ");
        if (parts.length != 3 || !parts[2].startsWith("HTTP/1.")) {
            throw new BridgeException(400, "Invalid HTTP request line");
        }
        Map<String, String> headers = new LinkedHashMap<>();
        for (;;) {
            String line = readLine(input);
            if (line.isEmpty()) {
                break;
            }
            int separator = line.indexOf(':');
            if (separator <= 0) {
                throw new BridgeException(400, "Invalid HTTP header");
            }
            headers.put(line.substring(0, separator).trim().toLowerCase(Locale.ROOT),
                line.substring(separator + 1).trim());
        }
        int contentLength;
        try {
            contentLength = Integer.parseInt(headers.getOrDefault("content-length", "0"));
        } catch (NumberFormatException e) {
            throw new BridgeException(400, "Invalid Content-Length");
        }
        if (contentLength < 0 || contentLength > MAX_BODY) {
            throw new BridgeException(413, "Request body is too large");
        }
        byte[] body = input.readNBytes(contentLength);
        if (body.length != contentLength) {
            throw new EOFException("Incomplete HTTP request body");
        }
        String path = parts[1].split("\\?", 2)[0];
        return new HttpRequest(parts[0], path, headers, new String(body, StandardCharsets.UTF_8));
    }

    private static String readLine(BufferedInputStream input) throws IOException {
        ByteArrayOutputStream value = new ByteArrayOutputStream();
        int previous = -1;
        while (value.size() <= MAX_HEADER_LINE) {
            int current = input.read();
            if (current == -1) {
                throw new EOFException("Unexpected end of HTTP headers");
            }
            if (previous == '\r' && current == '\n') {
                byte[] bytes = value.toByteArray();
                return new String(bytes, 0, Math.max(0, bytes.length - 1), StandardCharsets.US_ASCII);
            }
            value.write(current);
            previous = current;
        }
        throw new BridgeException(431, "HTTP header line is too long");
    }

    private boolean authorized(String authorization) {
        if (authorization == null || !authorization.startsWith("Bearer ")) {
            return false;
        }
        byte[] expected = token.getBytes(StandardCharsets.UTF_8);
        byte[] actual = authorization.substring(7).getBytes(StandardCharsets.UTF_8);
        return MessageDigest.isEqual(expected, actual);
    }

    private void writeBridgeFile(int port) throws IOException {
        Files.createDirectories(runtimeDirectory);
        Map<String, Object> bridge = new LinkedHashMap<>();
        bridge.put("version", 1);
        bridge.put("host", "127.0.0.1");
        bridge.put("port", port);
        bridge.put("token", token);
        bridge.put("pid", ProcessHandle.current().pid());
        Path temporary = runtimeDirectory.resolve("bridge.json.tmp");
        Files.writeString(temporary, gson.toJson(bridge), StandardCharsets.UTF_8);
        try {
            Files.move(temporary, bridgeFile, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
        } catch (AtomicMoveNotSupportedException e) {
            Files.move(temporary, bridgeFile, StandardCopyOption.REPLACE_EXISTING);
        }
    }

    private void writeResponse(BufferedOutputStream output, int status, Map<String, Object> response) throws IOException {
        byte[] body = gson.toJson(response).getBytes(StandardCharsets.UTF_8);
        String statusText = switch (status) {
        case 200 -> "OK";
        case 400 -> "Bad Request";
        case 401 -> "Unauthorized";
        case 404 -> "Not Found";
        case 405 -> "Method Not Allowed";
        case 409 -> "Conflict";
        case 413 -> "Payload Too Large";
        case 431 -> "Request Header Fields Too Large";
        default -> "Internal Server Error";
        };
        String headers = "HTTP/1.1 " + status + " " + statusText + "\r\n"
            + "Content-Type: application/json; charset=utf-8\r\n"
            + "Content-Length: " + body.length + "\r\n"
            + "Connection: close\r\n"
            + "Cache-Control: no-store\r\n\r\n";
        output.write(headers.getBytes(StandardCharsets.US_ASCII));
        output.write(body);
        output.flush();
    }

    private static Map<String, Object> errorBody(String message) {
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("error", message);
        return result;
    }

    private static String requiredString(JsonObject body, String name) {
        String value = optionalString(body, name);
        if (value == null || value.isBlank()) {
            throw new BridgeException(400, name + " is required");
        }
        return value;
    }

    private static String optionalString(JsonObject body, String name) {
        return body.has(name) && !body.get(name).isJsonNull() ? body.get(name).getAsString() : null;
    }

    private static String createToken() {
        byte[] value = new byte[32];
        new SecureRandom().nextBytes(value);
        return Base64.getUrlEncoder().withoutPadding().encodeToString(value);
    }

    private static String safeMessage(Throwable error) {
        String message = error.getMessage();
        return message == null || message.isBlank() ? error.getClass().getSimpleName() : message;
    }

    @Override
    public void close() throws IOException {
        ServerSocket current = socket;
        socket = null;
        if (current != null) {
            current.close();
        }
        clients.shutdownNow();
        Files.deleteIfExists(bridgeFile);
    }

    private record HttpRequest(String method, String path, Map<String, String> headers, String body) {
    }

    private static final class DaemonThreadFactory implements ThreadFactory {
        private final AtomicInteger number = new AtomicInteger();

        @Override
        public Thread newThread(Runnable runnable) {
            Thread result = new Thread(runnable, "onec-edt-mcp-client-" + number.incrementAndGet());
            result.setDaemon(true);
            return result;
        }
    }
}
