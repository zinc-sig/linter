import com.sun.source.tree.ClassTree;
import com.sun.source.tree.CompilationUnitTree;
import com.sun.source.tree.Tree;
import com.sun.source.util.TreePath;
import com.sun.source.util.Trees;
import com.sun.tools.javac.api.JavacTaskImpl;
import com.sun.tools.javac.api.JavacTaskPool;
import com.sun.tools.javac.platform.JDKPlatformProvider;
import com.sun.tools.javac.platform.PlatformDescription;
import com.sun.tools.javac.platform.PlatformProvider;

import java.io.BufferedReader;
import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.BasicFileAttributes;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.stream.Collectors;
import java.util.stream.Stream;
import javax.lang.model.element.Element;
import javax.tools.Diagnostic;
import javax.tools.DiagnosticCollector;
import javax.tools.JavaFileManager;
import javax.tools.JavaFileObject;
import javax.tools.StandardJavaFileManager;
import javax.tools.StandardLocation;

/**
 * Resident javac worker (docs/CONTRACT.md Appendix A). Reads one JSON
 * request per stdin line, compiles the named files together with the other
 * Java sources of the request's workspace up to flow analysis, and writes
 * one JSON reply per stdout line holding the findings for the named files.
 * With --once it checks the files given as arguments against the current
 * directory instead and prints the report object to stdout.
 */
public final class JavacWorker {

    /** Replies only; System.out is pointed at stderr so nothing else lands here. */
    private static final OutputStream REPLIES = new FileOutputStream(FileDescriptor.out);

    private static final Object REPLY_LOCK = new Object();

    /** Sibling sources beyond this many make the request fail rather than run past its budget. */
    private static final int MAX_SIBLINGS = 500;

    /** Top-level workspace directories that hold no sources of the program. */
    private static final List<String> SKIPPED_DIRS = List.of(".zinc", "examination-assets");

    private static final String TOOL = "javac " + System.getProperty("java.version");

    /** Each pool thread keeps its own compiler state; none of it is shared. */
    private static final ThreadLocal<Compiler> COMPILER = ThreadLocal.withInitial(Compiler::new);

    private JavacWorker() {
    }

    public static void main(String[] args) throws Exception {
        System.setOut(new PrintStream(new FileOutputStream(FileDescriptor.err), true, StandardCharsets.UTF_8));

        int threads = 0;
        boolean once = false;
        String release = null;
        String warmup = null;
        final List<String> files = new ArrayList<>();
        for (int i = 0; i < args.length; i++) {
            if ("--threads".equals(args[i]) && i + 1 < args.length) {
                threads = Integer.parseInt(args[++i]);
            } else if ("--warmup".equals(args[i]) && i + 1 < args.length) {
                warmup = args[++i];
            } else if ("--release".equals(args[i]) && i + 1 < args.length) {
                release = args[++i];
            } else if ("--once".equals(args[i])) {
                once = true;
            } else if (once && !args[i].startsWith("--")) {
                files.add(args[i]);
            } else {
                usage("unknown argument " + args[i]);
            }
        }
        if (once) {
            if (release == null || files.isEmpty()) {
                usage("--once needs --release and at least one file");
            }
            try {
                final StringBuilder out = new StringBuilder(256);
                check(release, Path.of(""), files, out);
                REPLIES.write(out.append('\n').toString().getBytes(StandardCharsets.UTF_8));
                REPLIES.flush();
                System.exit(0);
            } catch (Exception ex) {
                System.err.println("javac-worker: " + causeChain(ex));
                System.exit(1);
            }
        }
        if (threads < 1) {
            usage("--threads (at least 1) is required");
        }

        final ExecutorService pool = Executors.newFixedThreadPool(threads, runnable -> {
            final Thread thread = new Thread(runnable, "javac-worker");
            thread.setDaemon(true);
            return thread;
        });
        if (warmup != null) {
            warm(pool, threads, warmup);
        }

        final BufferedReader in = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.UTF_8));
        String line;
        while ((line = in.readLine()) != null) {
            if (line.isEmpty()) {
                continue;
            }
            final long id;
            final String requestRelease;
            final Path dir;
            final List<String> requestFiles = new ArrayList<>();
            try {
                final Object parsed = new Json(line).parseDocument();
                if (!(parsed instanceof Map)) {
                    throw new IllegalArgumentException("request is not an object");
                }
                final Map<?, ?> request = (Map<?, ?>) parsed;
                id = requireId(request.get("id"));
                requestRelease = requireString(request.get("release"), "release");
                dir = Path.of(requireString(request.get("dir"), "dir"));
                final Object list = request.get("files");
                if (!(list instanceof List) || ((List<?>) list).isEmpty()) {
                    throw new IllegalArgumentException("files must be a non-empty array");
                }
                for (Object file : (List<?>) list) {
                    requestFiles.add(requireString(file, "files"));
                }
            } catch (RuntimeException ex) {
                // The server never sends this; exiting makes it restart us.
                System.err.println("javac-worker: malformed request: " + ex.getMessage());
                System.exit(3);
                return;
            }
            pool.execute(() -> handle(id, requestRelease, dir, requestFiles));
        }

        pool.shutdown();
        pool.awaitTermination(Long.MAX_VALUE, TimeUnit.DAYS);
        System.exit(0);
    }

    private static void usage(String message) {
        System.err.println("javac-worker: " + message);
        System.err.println("usage: JavacWorker --threads N [--warmup FILE]");
        System.err.println("       JavacWorker --once --release N FILE...");
        System.exit(2);
    }

    /**
     * Checks the warm-up sample once on every pool thread before the first
     * request is read, so no student pays for a cold compiler. The latch
     * keeps each thread busy until all of them have taken a task.
     */
    private static void warm(ExecutorService pool, int threads, String file) throws InterruptedException {
        final Path path = Path.of(file);
        final CountDownLatch started = new CountDownLatch(threads);
        final CountDownLatch finished = new CountDownLatch(threads);
        for (int i = 0; i < threads; i++) {
            pool.execute(() -> {
                started.countDown();
                try {
                    started.await();
                    check("11", path.getParent(), List.of(path.toString()), new StringBuilder());
                } catch (Exception ex) {
                    System.err.println("javac-worker: warm-up failed: " + causeChain(ex));
                } finally {
                    finished.countDown();
                }
            });
        }
        finished.await();
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

    private static String requireString(Object value, String field) {
        if (!(value instanceof String) || ((String) value).isEmpty()) {
            throw new IllegalArgumentException(field + " must hold non-empty strings");
        }
        return (String) value;
    }

    private static void handle(long id, String release, Path dir, List<String> files) {
        final StringBuilder reply = new StringBuilder(256);
        reply.append("{\"id\":").append(id);
        try {
            final StringBuilder report = new StringBuilder(256);
            check(release, dir, files, report);
            reply.append(",\"report\":").append(report);
        } catch (OutOfMemoryError oom) {
            // The heap may be left inconsistent; let the server restart us.
            System.err.println("javac-worker: out of memory on request " + id);
            Runtime.getRuntime().halt(4);
            return;
        } catch (Throwable ex) {
            reply.setLength(0);
            reply.append("{\"id\":").append(id).append(",\"error\":");
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

    /**
     * Compiles files with the other Java sources under dir and appends the
     * report object for files to out: {"tool": ..., "findings": [...]}, each
     * finding in the shape of contract section 2 with its path as given.
     */
    static void check(String release, Path dir, List<String> files, StringBuilder out) throws IOException {
        final Map<Path, String> reported = new LinkedHashMap<>();
        for (String file : files) {
            reported.put(dir.resolve(file).toAbsolutePath().normalize(), file);
        }
        // The named files go first, so a class a sibling declares again is
        // reported as a duplicate in the sibling, which is not shown.
        final List<Path> units = new ArrayList<>(reported.keySet());
        units.addAll(siblings(dir, reported));

        final List<Diagnostic<? extends JavaFileObject>> diagnostics =
                COMPILER.get().analyze(release, units, reported.keySet());

        out.append("{\"tool\":");
        Json.quote(out, TOOL + " --release " + release);
        out.append(",\"findings\":[");
        final Map<Path, Lines> lines = new HashMap<>();
        boolean first = true;
        for (Diagnostic<? extends JavaFileObject> d : diagnostics) {
            final String severity = severity(d.getKind());
            if (d.getSource() == null) {
                if (d.getKind() == Diagnostic.Kind.ERROR) {
                    throw new IOException("javac: " + d.getMessage(Locale.ENGLISH));
                }
                continue;
            }
            final Path source = Path.of(d.getSource().toUri()).normalize();
            final String path = reported.get(source);
            if (path == null || severity == null) {
                continue;
            }
            // A file that cannot be read fails the check, as it would for a
            // one-shot tool, instead of becoming a finding on itself.
            if ("compiler.err.error.reading.file".equals(d.getCode())) {
                throw new IOException("javac: " + d.getMessage(Locale.ENGLISH));
            }
            final Lines text = lines.computeIfAbsent(source, s -> new Lines(content(d.getSource())));
            if (!first) {
                out.append(',');
            }
            first = false;
            out.append("{\"path\":");
            Json.quote(out, path);
            final long pos = d.getPosition();
            if (pos == Diagnostic.NOPOS) {
                out.append(",\"line\":0");
            } else {
                out.append(",\"line\":").append(text.line(pos)).append(",\"column\":").append(text.column(pos));
                // A range that runs past its first line, such as a whole
                // class declaration, ends at that line's end.
                final long end = Math.min(d.getEndPosition(), text.lineEnd(pos));
                if (d.getEndPosition() != Diagnostic.NOPOS && end > pos) {
                    out.append(",\"end_line\":").append(text.line(end - 1))
                            .append(",\"end_column\":").append(text.column(end - 1));
                }
            }
            out.append(",\"severity\":\"").append(severity).append('"');
            out.append(",\"rule\":");
            Json.quote(out, d.getCode());
            out.append(",\"message\":");
            Json.quote(out, d.getMessage(Locale.ENGLISH).strip());
            out.append('}');
        }
        out.append("]}");
    }

    private static String severity(Diagnostic.Kind kind) {
        switch (kind) {
            case ERROR:
                return "error";
            case WARNING:
            case MANDATORY_WARNING:
                return "warning";
            default:
                return null;
        }
    }

    /** The other regular .java files under dir, in path order, without following links. */
    private static List<Path> siblings(Path dir, Map<Path, String> reported) throws IOException {
        final Path root = dir.toAbsolutePath().normalize();
        final List<Path> found;
        try (Stream<Path> walk = Files.find(root, Integer.MAX_VALUE,
                (p, attrs) -> attrs.isRegularFile() && p.getFileName().toString().endsWith(".java"))) {
            found = walk.map(Path::normalize)
                    .filter(p -> !reported.containsKey(p) && !skipped(root, p))
                    .sorted()
                    .collect(Collectors.toList());
        }
        if (found.size() > MAX_SIBLINGS) {
            throw new IOException("the workspace holds " + found.size() + " other Java sources, more than " + MAX_SIBLINGS);
        }
        return found;
    }

    private static boolean skipped(Path root, Path p) {
        final Path rel = root.relativize(p);
        return rel.getNameCount() > 1 && SKIPPED_DIRS.contains(rel.getName(0).toString());
    }

    private static String content(JavaFileObject source) {
        try {
            return source.getCharContent(true).toString();
        } catch (IOException ex) {
            return "";
        }
    }

    /**
     * The compiler state of one pool thread: a task pool that reuses javac's
     * symbol tables between requests, and per release a file manager over
     * that release's API signatures (the ct.sym data --release reads), built
     * once instead of on every task. User sources only ever reach javac as
     * compilation units, which the task pool forgets after each task;
     * nothing is looked up on a source or class path. Every unit is parsed
     * and entered, but only the classes of the reported files are
     * attributed and flow-analysed, so a sibling costs its declarations,
     * not its method bodies.
     */
    private static final class Compiler {
        private final JavacTaskPool pool = new JavacTaskPool(4);
        private final Map<String, JavaFileManager> platforms = new HashMap<>();
        private final PlatformProvider provider = new JDKPlatformProvider();

        List<Diagnostic<? extends JavaFileObject>> analyze(String release, List<Path> units, java.util.Set<Path> reported)
                throws IOException {
            final StandardJavaFileManager fm = (StandardJavaFileManager) platform(release);
            final List<String> options = List.of(
                    "-source", release, "-target", release,
                    "-proc:none", "-implicit:none", "-encoding", "UTF-8",
                    "-Xlint:-options", "-Xmaxerrs", "1000", "-Xmaxwarns", "1000");
            final DiagnosticCollector<JavaFileObject> collector = new DiagnosticCollector<>();
            final Iterable<? extends JavaFileObject> sources = fm.getJavaFileObjectsFromPaths(units);
            return pool.getTask(null, fm, collector, options, null, sources, task -> {
                final JavacTaskImpl impl = (JavacTaskImpl) task;
                final Trees trees = Trees.instance(task);
                final List<Element> classes = new ArrayList<>();
                final Iterable<? extends CompilationUnitTree> parsed = impl.parse();
                impl.enter();
                for (CompilationUnitTree unit : parsed) {
                    if (!reported.contains(Path.of(unit.getSourceFile().toUri()).normalize())) {
                        continue;
                    }
                    for (Tree decl : unit.getTypeDecls()) {
                        if (decl instanceof ClassTree) {
                            final Element element = trees.getElement(new TreePath(new TreePath(unit), decl));
                            if (element != null) {
                                classes.add(element);
                            }
                        }
                    }
                }
                impl.analyze(classes);
                return collector.getDiagnostics();
            });
        }

        private JavaFileManager platform(String release) throws IOException {
            JavaFileManager fm = platforms.get(release);
            if (fm != null) {
                return fm;
            }
            final List<String> supported = new ArrayList<>();
            provider.getSupportedPlatformNames().forEach(supported::add);
            if (!supported.contains(release)) {
                throw new IOException("release " + release + " is not supported by " + TOOL
                        + " (supported: " + String.join(", ", supported) + ")");
            }
            final PlatformDescription description;
            try {
                description = provider.getPlatform(release, "");
            } catch (PlatformProvider.PlatformNotSupported ex) {
                throw new IOException("release " + release + " is not supported by " + TOOL);
            }
            fm = description.getFileManager();
            final StandardJavaFileManager standard = (StandardJavaFileManager) fm;
            standard.setLocation(StandardLocation.CLASS_PATH, List.of());
            standard.setLocation(StandardLocation.SOURCE_PATH, List.of());
            platforms.put(release, fm);
            return fm;
        }
    }

    /** Line starts of one source, for javac's character offsets. */
    private static final class Lines {
        private final String text;
        private final List<Integer> starts = new ArrayList<>();

        Lines(String text) {
            this.text = text;
            starts.add(0);
            for (int i = 0; i < text.length(); i++) {
                final char c = text.charAt(i);
                if (c == '\n' || c == '\r' && (i + 1 >= text.length() || text.charAt(i + 1) != '\n')) {
                    starts.add(i + 1);
                }
            }
        }

        private int index(long pos) {
            int lo = 0;
            int hi = starts.size() - 1;
            while (lo < hi) {
                final int mid = (lo + hi + 1) >>> 1;
                if (starts.get(mid) <= pos) {
                    lo = mid;
                } else {
                    hi = mid - 1;
                }
            }
            return lo;
        }

        /** The offset just past the last character of pos's line, before its terminator. */
        long lineEnd(long pos) {
            final int i = index(pos);
            if (i + 1 >= starts.size()) {
                return text.length();
            }
            final int next = starts.get(i + 1);
            final boolean crlf = next >= 2 && text.charAt(next - 2) == '\r' && text.charAt(next - 1) == '\n';
            return next - (crlf ? 2 : 1);
        }

        /** The 1-based line of a character offset. */
        int line(long pos) {
            return index(pos) + 1;
        }

        /** The 1-based column of a character offset, counting code points and a tab as one. */
        int column(long pos) {
            final int start = starts.get(index(pos));
            final int at = (int) Math.min(pos, text.length());
            return text.codePointCount(start, Math.max(start, at)) + 1;
        }
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
