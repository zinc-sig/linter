import com.puppycrawl.tools.checkstyle.AbstractAutomaticBean;
import com.puppycrawl.tools.checkstyle.Checker;
import com.puppycrawl.tools.checkstyle.ConfigurationLoader;
import com.puppycrawl.tools.checkstyle.PackageObjectFactory;
import com.puppycrawl.tools.checkstyle.PropertiesExpander;
import com.puppycrawl.tools.checkstyle.ThreadModeSettings;
import com.puppycrawl.tools.checkstyle.XMLLogger;
import com.puppycrawl.tools.checkstyle.api.Configuration;
import com.puppycrawl.tools.checkstyle.api.RootModule;

import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

/**
 * Resident checkstyle worker (docs/CONTRACT.md Appendix A). Reads one JSON
 * request per stdin line, lints the named files on a fresh Checker built
 * from the configuration loaded at launch, and writes one JSON reply per
 * stdout line holding the XMLLogger report the CLI's "-f xml" prints.
 */
public final class CheckstyleWorker {

    /** Replies only; System.out is pointed at stderr so nothing else lands here. */
    private static final OutputStream REPLIES = new FileOutputStream(FileDescriptor.out);

    private static final Object REPLY_LOCK = new Object();

    private CheckstyleWorker() {
    }

    public static void main(String[] args) throws Exception {
        System.setOut(new PrintStream(new FileOutputStream(FileDescriptor.err), true, StandardCharsets.UTF_8));

        String configPath = null;
        int threads = 0;
        for (int i = 0; i < args.length; i++) {
            if ("--config".equals(args[i]) && i + 1 < args.length) {
                configPath = args[++i];
            } else if ("--threads".equals(args[i]) && i + 1 < args.length) {
                threads = Integer.parseInt(args[++i]);
            } else {
                usage("unknown argument " + args[i]);
            }
        }
        if (configPath == null || threads < 1) {
            usage("--config and --threads (at least 1) are required");
        }

        // Same loading as checkstyle's Main: system properties expand
        // ${...} references and modules marked ignore are omitted.
        final Configuration config = ConfigurationLoader.loadConfiguration(
                configPath, new PropertiesExpander(System.getProperties()),
                ConfigurationLoader.IgnoredModulesOptions.OMIT,
                ThreadModeSettings.SINGLE_THREAD_MODE_INSTANCE);

        final ExecutorService pool = Executors.newFixedThreadPool(threads, runnable -> {
            final Thread thread = new Thread(runnable, "checkstyle-worker");
            thread.setDaemon(true);
            return thread;
        });

        final BufferedReader in = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
        String line;
        while ((line = in.readLine()) != null) {
            if (line.isEmpty()) {
                continue;
            }
            final long id;
            final List<File> files = new ArrayList<>();
            try {
                final Object parsed = new Json(line).parseDocument();
                if (!(parsed instanceof Map)) {
                    throw new IllegalArgumentException("request is not an object");
                }
                final Map<?, ?> request = (Map<?, ?>) parsed;
                id = requireId(request.get("id"));
                final Object list = request.get("files");
                if (!(list instanceof List) || ((List<?>) list).isEmpty()) {
                    throw new IllegalArgumentException("files must be a non-empty array");
                }
                for (Object file : (List<?>) list) {
                    if (!(file instanceof String)) {
                        throw new IllegalArgumentException("files must hold strings");
                    }
                    files.add(new File((String) file));
                }
            } catch (RuntimeException ex) {
                // The server never sends this; exiting makes it restart us.
                System.err.println("checkstyle-worker: malformed request: " + ex.getMessage());
                System.exit(3);
                return;
            }
            pool.execute(() -> handle(config, id, files));
        }

        pool.shutdown();
        pool.awaitTermination(Long.MAX_VALUE, TimeUnit.DAYS);
        System.exit(0);
    }

    private static void usage(String message) {
        System.err.println("checkstyle-worker: " + message);
        System.err.println("usage: CheckstyleWorker --config PATH --threads N");
        System.exit(2);
    }

    private static long requireId(Object value) {
        if (value instanceof Double) {
            final double d = (Double) value;
            if (d >= 1 && d <= 9007199254740991.0 && d == Math.rint(d)) {
                return (long) d;
            }
        }
        throw new IllegalArgumentException("id must be a positive integer");
    }

    private static void handle(Configuration config, long id, List<File> files) {
        final StringBuilder reply = new StringBuilder(256);
        reply.append("{\"id\":").append(id);
        try {
            final String xml = check(config, files);
            reply.append(",\"xml\":");
            Json.quote(reply, xml);
        } catch (OutOfMemoryError oom) {
            // The heap may be left inconsistent; let the server restart us.
            System.err.println("checkstyle-worker: out of memory on request " + id);
            Runtime.getRuntime().halt(4);
            return;
        } catch (Throwable ex) {
            reply.append(",\"error\":");
            Json.quote(reply, causeChain(ex));
        }
        reply.append("}\n");
        final byte[] bytes = reply.toString().getBytes(StandardCharsets.UTF_8);
        synchronized (REPLY_LOCK) {
            try {
                REPLIES.write(bytes);
                REPLIES.flush();
            } catch (IOException ex) {
                // The server is gone; nothing left to answer.
                Runtime.getRuntime().halt(5);
            }
        }
    }

    /** Runs one audit the way checkstyle's Main does, on a fresh Checker. */
    private static String check(Configuration config, List<File> files) throws Exception {
        final ClassLoader loader = Checker.class.getClassLoader();
        final PackageObjectFactory factory = new PackageObjectFactory(Checker.class.getPackage().getName(), loader);
        final RootModule root = (RootModule) factory.createModule(config.getName());
        final ByteArrayOutputStream out = new ByteArrayOutputStream(1024);
        try {
            root.setModuleClassLoader(loader);
            root.configure(config);
            root.addListener(new XMLLogger(out, AbstractAutomaticBean.OutputStreamOptions.CLOSE));
            root.process(files);
        } finally {
            root.destroy();
        }
        return out.toString(StandardCharsets.UTF_8);
    }

    private static String causeChain(Throwable ex) {
        final StringBuilder sb = new StringBuilder();
        for (Throwable t = ex; t != null; t = t.getCause()) {
            if (sb.length() > 0) {
                sb.append("\nCaused by: ");
            }
            sb.append(t.getClass().getName());
            if (t.getMessage() != null) {
                sb.append(": ").append(t.getMessage());
            }
            if (t.getCause() == t) {
                break;
            }
        }
        return sb.toString();
    }

    /** Minimal JSON reader and string quoting; the jlink'ed runtime has no JSON library. */
    static final class Json {
        private final String s;
        private int pos;

        Json(String s) {
            this.s = s;
        }

        Object parseDocument() {
            final Object value = parseValue();
            skipWhitespace();
            if (pos != s.length()) {
                throw error("trailing data");
            }
            return value;
        }

        private Object parseValue() {
            skipWhitespace();
            if (pos >= s.length()) {
                throw error("unexpected end");
            }
            final char c = s.charAt(pos);
            switch (c) {
                case '{':
                    return parseObject();
                case '[':
                    return parseArray();
                case '"':
                    return parseString();
                case 't':
                    expectWord("true");
                    return Boolean.TRUE;
                case 'f':
                    expectWord("false");
                    return Boolean.FALSE;
                case 'n':
                    expectWord("null");
                    return null;
                default:
                    if (c == '-' || c >= '0' && c <= '9') {
                        return parseNumber();
                    }
                    throw error("unexpected character");
            }
        }

        private Map<String, Object> parseObject() {
            final Map<String, Object> map = new LinkedHashMap<>();
            pos++;
            skipWhitespace();
            if (peek('}')) {
                pos++;
                return map;
            }
            while (true) {
                skipWhitespace();
                if (!peek('"')) {
                    throw error("expected key");
                }
                final String key = parseString();
                skipWhitespace();
                expect(':');
                map.put(key, parseValue());
                skipWhitespace();
                if (peek(',')) {
                    pos++;
                } else {
                    expect('}');
                    return map;
                }
            }
        }

        private List<Object> parseArray() {
            final List<Object> list = new ArrayList<>();
            pos++;
            skipWhitespace();
            if (peek(']')) {
                pos++;
                return list;
            }
            while (true) {
                list.add(parseValue());
                skipWhitespace();
                if (peek(',')) {
                    pos++;
                } else {
                    expect(']');
                    return list;
                }
            }
        }

        private String parseString() {
            pos++;
            final StringBuilder sb = new StringBuilder();
            while (true) {
                if (pos >= s.length()) {
                    throw error("unterminated string");
                }
                final char c = s.charAt(pos++);
                if (c == '"') {
                    return sb.toString();
                }
                if (c < 0x20) {
                    throw error("control character in string");
                }
                if (c != '\\') {
                    sb.append(c);
                    continue;
                }
                if (pos >= s.length()) {
                    throw error("unterminated escape");
                }
                final char e = s.charAt(pos++);
                switch (e) {
                    case '"':
                    case '\\':
                    case '/':
                        sb.append(e);
                        break;
                    case 'b':
                        sb.append('\b');
                        break;
                    case 'f':
                        sb.append('\f');
                        break;
                    case 'n':
                        sb.append('\n');
                        break;
                    case 'r':
                        sb.append('\r');
                        break;
                    case 't':
                        sb.append('\t');
                        break;
                    case 'u':
                        if (pos + 4 > s.length()) {
                            throw error("short unicode escape");
                        }
                        try {
                            sb.append((char) Integer.parseInt(s.substring(pos, pos + 4), 16));
                        } catch (NumberFormatException ex) {
                            throw error("bad unicode escape");
                        }
                        pos += 4;
                        break;
                    default:
                        throw error("bad escape");
                }
            }
        }

        private Double parseNumber() {
            final int start = pos;
            while (pos < s.length() && "+-0123456789.eE".indexOf(s.charAt(pos)) >= 0) {
                pos++;
            }
            try {
                return Double.valueOf(s.substring(start, pos));
            } catch (NumberFormatException ex) {
                throw error("bad number");
            }
        }

        private void expectWord(String word) {
            if (!s.startsWith(word, pos)) {
                throw error("unexpected literal");
            }
            pos += word.length();
        }

        private void expect(char c) {
            if (!peek(c)) {
                throw error("expected '" + c + "'");
            }
            pos++;
        }

        private boolean peek(char c) {
            return pos < s.length() && s.charAt(pos) == c;
        }

        private void skipWhitespace() {
            while (pos < s.length() && " \t\r\n".indexOf(s.charAt(pos)) >= 0) {
                pos++;
            }
        }

        private IllegalArgumentException error(String message) {
            return new IllegalArgumentException(message + " at offset " + pos);
        }

        private static final char[] HEX = "0123456789abcdef".toCharArray();

        /** Appends s as a JSON string literal; everything outside printable ASCII is escaped. */
        static void quote(StringBuilder out, String s) {
            out.append('"');
            for (int i = 0; i < s.length(); i++) {
                final char c = s.charAt(i);
                switch (c) {
                    case '"':
                        out.append("\\\"");
                        break;
                    case '\\':
                        out.append("\\\\");
                        break;
                    case '\n':
                        out.append("\\n");
                        break;
                    case '\r':
                        out.append("\\r");
                        break;
                    case '\t':
                        out.append("\\t");
                        break;
                    default:
                        if (c < 0x20 || c >= 0x7f) {
                            out.append("\\u").append(HEX[c >> 12 & 0xf]).append(HEX[c >> 8 & 0xf])
                                    .append(HEX[c >> 4 & 0xf]).append(HEX[c & 0xf]);
                        } else {
                            out.append(c);
                        }
                }
            }
            out.append('"');
        }
    }
}
