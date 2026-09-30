import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import org.jetbrains.kotlin.analysis.api.diagnostics.KaSeverity
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.withLock
import kotlin.system.exitProcess
import kotlin.system.measureTimeMillis

// The analyzer process: requests and responses are JSON objects, one per
// line, on stdin and stdout. Logs go to stderr.

class Analyzer {
    private var model: Model? = null
    private var jdkHome = ""
    private var ws: Workspace? = null
    private var byPath: Map<String, org.jetbrains.kotlin.psi.KtFile> = emptyMap()

    private fun open(): JsonObject {
        ws?.close()
        lateinit var fresh: Workspace
        val ms = measureTimeMillis { fresh = Workspace(model!!, jdkHome) }
        ws = fresh
        byPath = fresh.files.associateBy { it.virtualFile.path }
        return buildJsonObject {
            put("files", fresh.files.size)
            put("millis", ms)
        }
    }

    fun handle(method: String, params: JsonObject, queue: Queue): JsonElement = when (method) {
        "init" -> {
            model = Model(params.string("model"))
            jdkHome = params.string("jdkHome")
            open()
        }
        "rebuild" -> open()
        "check" -> check(params)
        "hover" -> {
            val path = params.string("path")
            val file = byPath[path] ?: error("not in the project: $path")
            val copy = ws!!.copyOf(file, params.string("text"))
            hover(copy, params.string("offset").toInt())?.let { hoverResult(it) } ?: JsonNull
        }
        "diagnose" -> {
            val paths = params["paths"]?.let { if (it is JsonNull) null else it.jsonArray.map { p -> p.jsonPrimitive.content } }
            val files = if (paths.isNullOrEmpty()) byPath.values.toList() else paths.mapNotNull { byPath[it] }
            buildJsonObject {
                put("files", buildJsonArray {
                    for (f in files) {
                        queue.runChecks(this@Analyzer) // checks don't wait for the whole batch
                        val ds = diagnose(f)
                        if (ds.isNotEmpty()) add(fileResult(f.virtualFile.path, ds))
                    }
                })
            }
        }
        else -> error("unknown method $method")
    }

    fun check(params: JsonObject): JsonObject {
        val path = params.string("path")
        val file = byPath[path] ?: error("not in the project: $path")
        lateinit var ds: List<Diag>
        val ms = measureTimeMillis { ds = ws!!.check(file, params.string("text")) }
        return fileResult(path, ds, ms)
    }

    private fun hoverResult(h: HoverInfo) = buildJsonObject {
        put("start", h.start)
        put("end", h.end)
        put("signature", h.signature)
        h.call?.let { put("call", it) }
        h.container?.let { put("container", it) }
        h.doc?.let { put("doc", it) }
        h.docLanguage?.let { put("docLanguage", it) }
        h.source?.let { src ->
            put("source", buildJsonObject {
                put("path", src.path)
                src.jar?.let { put("jar", it) }
                src.entry?.let { put("entry", it) }
                put("offset", src.offset)
            })
        }
    }

    private fun fileResult(path: String, ds: List<Diag>, millis: Long? = null) = buildJsonObject {
        put("path", path)
        if (millis != null) put("millis", millis)
        put("diagnostics", JsonArray(ds.map { d ->
            buildJsonObject {
                put("severity", when (d.severity) {
                    KaSeverity.ERROR -> "error"
                    KaSeverity.WARNING -> "warning"
                    else -> "info"
                })
                put("start", d.start)
                put("end", d.end)
                put("message", d.message)
                put("factory", d.factory)
            }
        }))
    }
}

private fun JsonObject.string(key: String) = this[key]?.jsonPrimitive?.content ?: error("missing $key")

class Request(val id: Int, val method: String, val params: JsonObject)

// Requests waiting to run, one at a time. Interactive requests (checks of
// what the user is typing, hovers) go first, and a newer check of a file
// replaces a waiting older one.
class Queue(private val out: java.io.PrintStream) {
    private val lock = ReentrantLock()
    private val arrived = lock.newCondition()
    private val checks = LinkedHashMap<String, Request>()
    private val interactive = ArrayDeque<Request>()
    private val others = ArrayDeque<Request>()

    fun put(r: Request) = lock.withLock {
        when (r.method) {
            "check" -> {
                val path = r.params["path"]?.jsonPrimitive?.content ?: ""
                checks.remove(path)?.let { respond(it.id, error = "superseded") }
                checks[path] = r
            }
            "hover" -> interactive.addLast(r)
            else -> others.addLast(r)
        }
        arrived.signalAll()
    }

    // take waits for the next request.
    fun take(): Request = lock.withLock {
        while (true) {
            nextCheck()?.let { return it }
            others.removeFirstOrNull()?.let { return it }
            arrived.await()
        }
        @Suppress("UNREACHABLE_CODE")
        error("unreachable")
    }

    // nextCheck returns the next interactive request.
    private fun nextCheck(): Request? = lock.withLock {
        interactive.removeFirstOrNull()?.let { return it }
        val first = checks.keys.firstOrNull() ?: return null
        checks.remove(first)
    }

    // runChecks runs the interactive requests waiting now (between the
    // files of a long request).
    fun runChecks(analyzer: Analyzer) {
        while (true) {
            val r = nextCheck() ?: return
            run(analyzer, r)
        }
    }

    fun run(analyzer: Analyzer, r: Request) {
        try {
            respond(r.id, result = analyzer.handle(r.method, r.params, this))
        } catch (t: Throwable) {
            System.err.println("analyzer: ${r.method} failed: $t")
            respond(r.id, error = t.message ?: t.toString())
        }
    }

    fun respond(id: Int, result: JsonElement? = null, error: String? = null) {
        val resp = buildJsonObject {
            put("id", id)
            if (error != null) put("error", error) else put("result", result ?: JsonNull)
        }
        synchronized(out) {
            out.println(resp.toString())
            out.flush()
        }
    }
}

fun main() {
    val analyzer = Analyzer()
    val out = System.out
    System.setOut(System.err) // nothing but responses may reach stdout
    val queue = Queue(out)
    Thread {
        val reader = System.`in`.bufferedReader()
        while (true) {
            val line = reader.readLine() ?: break
            if (line.isBlank()) continue
            val req = Json.parseToJsonElement(line).jsonObject
            val method = req["method"]?.jsonPrimitive?.content ?: ""
            if (method == "shutdown") exitProcess(0) // even in the middle of a request
            val id = req["id"]?.jsonPrimitive?.int ?: 0
            queue.put(Request(id, method, (req["params"] as? JsonObject) ?: JsonObject(emptyMap())))
        }
        exitProcess(0) // ktpls is gone
    }.apply { isDaemon = true; name = "ktpls-reader" }.start()
    while (true) queue.run(analyzer, queue.take())
}
